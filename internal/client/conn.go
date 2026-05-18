package client

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kiiimatz/tailway/internal/proto"
)

// handleNewConn dispatches to the appropriate handler based on protocol.
// Always called in its own goroutine to avoid blocking the read loop.
func (c *Client) handleNewConn(p proto.NewConnPayload) {
	c.mu.RLock()
	entry, ok := c.tunnels[p.TunnelID]
	c.mu.RUnlock()
	if !ok {
		return
	}

	switch p.Protocol {
	case "tcp", "http", "https":
		// For https: TLS is already terminated at the server side.
		// The data conn carries decrypted plaintext to the local service.
		c.handleTCP(p.ConnID, entry.ClientPort, &entry.Bytes)
	case "udp", "quic":
		c.handleUDP(p.ConnID, entry.ClientPort, p.UDPData, &entry.Bytes)
	case "socks5":
		c.handleSocks5(p.ConnID, p.SOCKS5Dest, &entry.Bytes)
	}
}

// handleTCP opens a data connection to the server and proxies it to the local
// TCP service on clientPort, counting bytes in both directions.
func (c *Client) handleTCP(connID string, clientPort int, bytes *int64) {
	dataConn, err := c.openDataConn(connID)
	if err != nil {
		return
	}

	localConn, err := net.DialTimeout("tcp",
		fmt.Sprintf("localhost:%d", clientPort), 10*time.Second)
	if err != nil {
		dataConn.Close()
		return
	}
	if tc, ok := localConn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	proxyConnsCount(dataConn, localConn, bytes)
}

// handleUDP opens a data connection to the server and relays UDP packets to
// the local UDP service on clientPort, counting bytes in both directions.
func (c *Client) handleUDP(connID string, clientPort int, firstPkt []byte, bytes *int64) {
	dataConn, err := c.openDataConn(connID)
	if err != nil {
		return
	}
	defer dataConn.Close()

	localAddr, err := net.ResolveUDPAddr("udp",
		fmt.Sprintf("localhost:%d", clientPort))
	if err != nil {
		return
	}
	localUDP, err := net.DialUDP("udp", nil, localAddr)
	if err != nil {
		return
	}
	defer localUDP.Close()

	if len(firstPkt) > 0 {
		atomic.AddInt64(bytes, int64(len(firstPkt)))
		localUDP.Write(firstPkt)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// A: data conn frames → local UDP service
	go func() {
		defer wg.Done()
		for {
			data, err := proto.ReadFrame(dataConn)
			if err != nil {
				return
			}
			atomic.AddInt64(bytes, int64(len(data)))
			localUDP.Write(data)
		}
	}()

	// B: local UDP replies → data conn frames
	go func() {
		defer wg.Done()
		buf := make([]byte, 65535)
		for {
			n, err := localUDP.Read(buf)
			if err != nil {
				return
			}
			atomic.AddInt64(bytes, int64(n))
			if err := proto.WriteFrame(dataConn, buf[:n]); err != nil {
				return
			}
		}
	}()

	wg.Wait()
}

// handleSocks5 opens a data connection to the server and dials dest directly,
// counting bytes in both directions.
func (c *Client) handleSocks5(connID string, dest string, bytes *int64) {
	if dest == "" {
		return
	}

	dataConn, err := c.openDataConn(connID)
	if err != nil {
		return
	}

	destConn, err := net.DialTimeout("tcp", dest, 15*time.Second)
	if err != nil {
		dataConn.Close()
		return
	}

	proxyConnsCount(dataConn, destConn, bytes)
}

// openDataConn dials the server's data port and identifies the connection.
func (c *Client) openDataConn(connID string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp",
		fmt.Sprintf("%s:%d", c.serverHost, dataPort), 10*time.Second)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	if err := proto.Send(conn, proto.TypeDataConn,
		proto.DataConnPayload{ConnID: connID}); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// countWriter wraps an io.Writer and atomically increments a counter on each
// Write call, so bytes are counted as they flow rather than only at EOF.
type countWriter struct {
	w     io.Writer
	bytes *int64
}

func (cw countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 {
		atomic.AddInt64(cw.bytes, int64(n))
	}
	return n, err
}

// proxyConnsCount runs a bidirectional pipe between two TCP connections,
// incrementing bytes on every write so the live byte count updates
// in real time rather than only after the connection closes.
func proxyConnsCount(a, b net.Conn, bytes *int64) {
	defer a.Close()
	defer b.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	half := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(countWriter{dst, bytes}, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}

	go half(a, b)
	go half(b, a)
	wg.Wait()
}
