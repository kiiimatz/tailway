package server

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/kiiimatz/tailway/internal/proto"
)

const pendingTimeout = 30 * time.Second

// pendingConn is created when an external connection arrives and we are waiting
// for the client to open the matching data connection.
type pendingConn struct {
	// TCP fields
	extConn net.Conn // nil for UDP sessions

	// UDP fields
	udpSrcAddr *net.UDPAddr // nil for TCP sessions
	udpChan    chan []byte  // subsequent UDP packets for this session

	tunnelID string
	created  time.Time
}

// isUDP reports whether this is a UDP pending connection.
func (pc *pendingConn) isUDP() bool { return pc.udpSrcAddr != nil }

// ─── TCP ──────────────────────────────────────────────────────────────────────

// serveTCP accepts external TCP connections and notifies the client.
func (s *Server) serveTCP(t *tunnel, client *clientConn) {
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.done:
			default:
				s.logf("tunnel %s accept error: %v", t.info.ID, err)
			}
			return
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
		go s.notifyNewTCP(conn, t, client)
	}
}

// notifyNewTCP stores the external connection and signals the client to open a
// data connection.  The pending entry is stored BEFORE sending new_conn to
// avoid a race where the client responds before we register it.
func (s *Server) notifyNewTCP(extConn net.Conn, t *tunnel, client *clientConn) {
	connID := uuid.New().String()

	s.mu.Lock()
	s.pending[connID] = &pendingConn{
		extConn:  extConn,
		tunnelID: t.info.ID,
		created:  time.Now(),
	}
	s.mu.Unlock()

	if err := client.send(proto.TypeNewConn, proto.NewConnPayload{
		ConnID:   connID,
		TunnelID: t.info.ID,
		Protocol: t.info.Protocol,
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, connID)
		s.mu.Unlock()
		extConn.Close()
	}
}

// ─── Data connection ──────────────────────────────────────────────────────────

// handleData pairs an incoming client data connection with its pending entry.
func (s *Server) handleData(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	msg, err := proto.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})

	if msg.Type != proto.TypeDataConn {
		conn.Close()
		return
	}
	var p proto.DataConnPayload
	if err := proto.Decode(msg, &p); err != nil {
		conn.Close()
		return
	}

	s.mu.Lock()
	pc, ok := s.pending[p.ConnID]
	if ok {
		delete(s.pending, p.ConnID)
	}
	s.mu.Unlock()

	if !ok {
		conn.Close()
		return
	}

	if pc.isUDP() {
		go s.relayUDP(pc, conn)
	} else {
		proxyCount(pc.extConn, conn, &s.totalBytes)
	}
}

// ─── UDP ──────────────────────────────────────────────────────────────────────

// serveUDP reads UDP packets and routes them to per-source-address sessions.
func (s *Server) serveUDP(t *tunnel, client *clientConn) {
	type session struct {
		toClient chan []byte
		lastSeen time.Time
	}

	var mu sync.Mutex
	sessions := make(map[string]*session)
	buf := make([]byte, 65535)

	// Idle session cleanup goroutine.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				cutoff := time.Now().Add(-60 * time.Second)
				mu.Lock()
				for addr, sess := range sessions {
					if sess.lastSeen.Before(cutoff) {
						close(sess.toClient)
						delete(sessions, addr)
					}
				}
				mu.Unlock()
			}
		}
	}()

	for {
		n, addr, err := t.udpConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-t.done:
			default:
				s.logf("tunnel %s udp read: %v", t.info.ID, err)
			}
			return
		}

		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		addrStr := addr.String()

		mu.Lock()
		sess, exists := sessions[addrStr]
		if !exists {
			sess = &session{toClient: make(chan []byte, 64)}
			sessions[addrStr] = sess
			go s.handleUDPSession(t, client, addr, sess.toClient)
		}
		sess.lastSeen = time.Now()
		mu.Unlock()

		select {
		case sess.toClient <- pkt:
		default:
			// Drop if channel is full.
		}
	}
}

// handleUDPSession waits for the first packet, creates a pending entry, and
// notifies the client.  Subsequent packets are forwarded by relayUDP via the
// same channel.
func (s *Server) handleUDPSession(
	t *tunnel,
	client *clientConn,
	srcAddr *net.UDPAddr,
	incoming chan []byte,
) {
	firstPkt, ok := <-incoming
	if !ok {
		return
	}

	connID := uuid.New().String()

	s.mu.Lock()
	s.pending[connID] = &pendingConn{
		udpSrcAddr: srcAddr,
		udpChan:    incoming,
		tunnelID:   t.info.ID,
		created:    time.Now(),
	}
	s.mu.Unlock()

	if err := client.send(proto.TypeNewConn, proto.NewConnPayload{
		ConnID:     connID,
		TunnelID:   t.info.ID,
		Protocol:   t.info.Protocol,
		UDPData:    firstPkt,
		UDPSrcAddr: srcAddr.String(),
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, connID)
		s.mu.Unlock()
	}
}

// relayUDP runs after a data connection is established for a UDP session.
func (s *Server) relayUDP(pc *pendingConn, dataConn net.Conn) {
	defer dataConn.Close()

	s.mu.RLock()
	t, ok := s.tunnels[pc.tunnelID]
	s.mu.RUnlock()
	if !ok {
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// A: incoming UDP packets → data conn
	go func() {
		defer wg.Done()
		for pkt := range pc.udpChan {
			atomic.AddInt64(&s.totalBytes, int64(len(pkt)))
			if err := proto.WriteFrame(dataConn, pkt); err != nil {
				return
			}
		}
	}()

	// B: data conn frames → send back to UDP source
	go func() {
		defer wg.Done()
		for {
			data, err := proto.ReadFrame(dataConn)
			if err != nil {
				return
			}
			atomic.AddInt64(&s.totalBytes, int64(len(data)))
			t.udpConn.WriteToUDP(data, pc.udpSrcAddr)
		}
	}()

	wg.Wait()
}

// ─── SOCKS5 ───────────────────────────────────────────────────────────────────

// serveSocks5 accepts TCP connections, performs the SOCKS5 handshake, then
// hands them off to the tailway client for outbound dialing.
func (s *Server) serveSocks5(t *tunnel, client *clientConn) {
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.done:
			default:
				s.logf("socks5 tunnel %s accept error: %v", t.info.ID, err)
			}
			return
		}
		go s.handleSocks5Conn(conn, t, client)
	}
}

// handleSocks5Conn performs the SOCKS5 handshake, then stores the established
// connection as a pending entry and notifies the client.
func (s *Server) handleSocks5Conn(conn net.Conn, t *tunnel, client *clientConn) {
	dest, err := socks5Handshake(conn)
	if err != nil {
		s.logf("socks5 handshake error: %v", err)
		conn.Close()
		return
	}

	connID := uuid.New().String()

	s.mu.Lock()
	s.pending[connID] = &pendingConn{
		extConn:  conn,
		tunnelID: t.info.ID,
		created:  time.Now(),
	}
	s.mu.Unlock()

	if err := client.send(proto.TypeNewConn, proto.NewConnPayload{
		ConnID:     connID,
		TunnelID:   t.info.ID,
		Protocol:   "socks5",
		SOCKS5Dest: dest,
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, connID)
		s.mu.Unlock()
		conn.Close()
	}
}

// socks5Handshake performs the server side of the SOCKS5 protocol (RFC 1928).
// It supports no-auth and CONNECT only. Returns "host:port" of the destination.
func socks5Handshake(conn net.Conn) (string, error) {
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	defer conn.SetDeadline(time.Time{})

	// ── Greeting ──────────────────────────────────────────────────────────────
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return "", fmt.Errorf("read greeting: %w", err)
	}
	if hdr[0] != 0x05 {
		return "", fmt.Errorf("not SOCKS5 (ver=%d)", hdr[0])
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", fmt.Errorf("read methods: %w", err)
	}
	// Reply: version=5, method=0 (no authentication)
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return "", err
	}

	// ── Request ───────────────────────────────────────────────────────────────
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return "", fmt.Errorf("read request: %w", err)
	}
	if req[0] != 0x05 {
		return "", fmt.Errorf("unexpected VER in request: %d", req[0])
	}
	if req[1] != 0x01 { // CMD must be CONNECT
		// Reply: command not supported
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return "", fmt.Errorf("unsupported SOCKS5 command: %d", req[1])
	}

	// ── Address ───────────────────────────────────────────────────────────────
	var host string
	switch req[3] { // ATYP
	case 0x01: // IPv4
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", err
		}
		host = net.IP(addr).String()

	case 0x03: // Domain name
		lenB := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenB); err != nil {
			return "", err
		}
		domain := make([]byte, lenB[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", err
		}
		host = string(domain)

	case 0x04: // IPv6
		addr := make([]byte, 16)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", err
		}
		host = "[" + net.IP(addr).String() + "]"

	default:
		conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return "", fmt.Errorf("unsupported address type: %d", req[3])
	}

	portB := make([]byte, 2)
	if _, err := io.ReadFull(conn, portB); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portB)

	// ── Reply: success ────────────────────────────────────────────────────────
	// BND.ADDR = 0.0.0.0, BND.PORT = 0
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%d", host, port), nil
}

// ─── Shared ───────────────────────────────────────────────────────────────────

// countWriter wraps an io.Writer and atomically increments a counter on each
// Write call, so bytes are counted as they flow rather than only at EOF.
type countWriter struct {
	w       io.Writer
	counter *int64
}

func (cw countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 {
		atomic.AddInt64(cw.counter, int64(n))
	}
	return n, err
}

// proxyCount runs a bidirectional pipe between two TCP connections,
// incrementing counter on every write so the live byte count updates
// in real time rather than only after the connection closes.
func proxyCount(a, b net.Conn, counter *int64) {
	defer a.Close()
	defer b.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	half := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(countWriter{dst, counter}, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}

	go half(a, b)
	go half(b, a)
	wg.Wait()
}

// sweepPending removes pending connections that were never fulfilled.
func (s *Server) sweepPending() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for id, pc := range s.pending {
			if now.Sub(pc.created) > pendingTimeout {
				delete(s.pending, id)
				if pc.extConn != nil {
					pc.extConn.Close()
				}
			}
		}
		s.mu.Unlock()
	}
}
