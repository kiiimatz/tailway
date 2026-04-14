package server

import (
	"io"
	"net"
	"sync"
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
		Protocol: "tcp",
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
		proxy(pc.extConn, conn) // closes both connections when done
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
			// Pass the channel so handleUDPSession can store it in pending,
			// and relayUDP can continue reading from it after the data conn arrives.
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

	// Store BEFORE sending new_conn (avoids the race described in notifyNewTCP).
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
		Protocol:   "udp",
		UDPData:    firstPkt,
		UDPSrcAddr: srcAddr.String(),
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, connID)
		s.mu.Unlock()
	}
	// Done — relayUDP takes over once the data conn arrives.
}

// relayUDP runs after a data connection is established for a UDP session.
//
//   server UDP port ←→ data conn (TCP) ←→ client ←→ local UDP service
//
// Direction A (server→client): packets from serveUDP arrive on pc.udpChan
//   and are written as raw frames to the data conn.
// Direction B (client→server): raw frames arrive on the data conn and are
//   written back to srcAddr via the tunnel's UDP socket.
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
			t.udpConn.WriteToUDP(data, pc.udpSrcAddr)
		}
	}()

	wg.Wait()
}

// ─── Shared ───────────────────────────────────────────────────────────────────

// proxy runs a bidirectional pipe between two TCP connections.
// Both connections are closed when the function returns.
func proxy(a, b net.Conn) {
	defer a.Close()
	defer b.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	half := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
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
