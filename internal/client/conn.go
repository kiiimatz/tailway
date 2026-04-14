package client

import (
	"fmt"
	"io"
	"net"
	"sync"

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
	case "tcp":
		c.handleTCP(p.ConnID, entry.ClientPort)
	case "udp":
		c.handleUDP(p.ConnID, entry.ClientPort, p.UDPData)
	}
}

// handleTCP opens a data connection to the server and proxies it to the local
// TCP service on clientPort.
func (c *Client) handleTCP(connID string, clientPort int) {
	dataConn, err := c.openDataConn(connID)
	if err != nil {
		return
	}

	localConn, err := net.DialTimeout("tcp",
		fmt.Sprintf("127.0.0.1:%d", clientPort), 10e9)
	if err != nil {
		dataConn.Close()
		return
	}

	proxyConns(dataConn, localConn)
}

// handleUDP opens a data connection to the server and relays UDP packets to
// the local UDP service on clientPort.
//
//   data conn (TCP) ←→ client ←→ localhost:clientPort (UDP)
//
// The first packet (from new_conn) is sent immediately.  Subsequent packets
// arrive as raw frames on the data conn and are forwarded the same way.
// Replies from the local service are written back as raw frames to the data conn.
func (c *Client) handleUDP(connID string, clientPort int, firstPkt []byte) {
	dataConn, err := c.openDataConn(connID)
	if err != nil {
		return
	}
	defer dataConn.Close()

	// Dial the local UDP service.
	localAddr, err := net.ResolveUDPAddr("udp",
		fmt.Sprintf("127.0.0.1:%d", clientPort))
	if err != nil {
		return
	}
	localUDP, err := net.DialUDP("udp", nil, localAddr)
	if err != nil {
		return
	}
	defer localUDP.Close()

	// Deliver the first packet that came inline with new_conn.
	if len(firstPkt) > 0 {
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
			if err := proto.WriteFrame(dataConn, buf[:n]); err != nil {
				return
			}
		}
	}()

	wg.Wait()
}

// openDataConn dials the server's data port and identifies the connection.
func (c *Client) openDataConn(connID string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp",
		fmt.Sprintf("%s:%d", c.serverHost, dataPort), 10e9)
	if err != nil {
		return nil, err
	}
	if err := proto.Send(conn, proto.TypeDataConn,
		proto.DataConnPayload{ConnID: connID}); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// proxyConns runs a bidirectional pipe between two TCP connections.
func proxyConns(a, b net.Conn) {
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
