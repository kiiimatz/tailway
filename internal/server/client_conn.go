package server

import (
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kiiimatz/tailway/internal/proto"
)

const pingInterval = 30 * time.Second

// clientConn represents an authenticated client on the control channel.
type clientConn struct {
	id   string
	conn net.Conn

	// writeMu serialises writes from keepalive, tunnel listeners, and the main loop.
	writeMu sync.Mutex

	// tunnels lists IDs owned by this client (guarded by mu).
	mu      sync.Mutex
	tunnels []string
}

// send writes a message to the client's control connection.
func (c *clientConn) send(msgType string, payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return proto.Send(c.conn, msgType, payload)
}

// handleControl runs the auth handshake and then the command read loop
// for a newly accepted control connection.
func (s *Server) handleControl(conn net.Conn) {
	defer conn.Close()

	// Auth — short deadline so stale connections don't linger.
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	msg, err := proto.ReadMessage(conn)
	if err != nil {
		return
	}
	conn.SetDeadline(time.Time{})

	if msg.Type != proto.TypeAuth {
		return
	}
	var auth proto.AuthPayload
	if err := proto.Decode(msg, &auth); err != nil || auth.Key != s.key {
		proto.Send(conn, proto.TypeAuthFail, proto.AuthFailPayload{Reason: "invalid key"})
		return
	}

	client := &clientConn{
		id:   uuid.New().String(),
		conn: conn,
	}

	s.mu.Lock()
	s.clients[client.id] = client
	s.mu.Unlock()

	if err := proto.Send(conn, proto.TypeAuthOK, nil); err != nil {
		s.mu.Lock()
		delete(s.clients, client.id)
		s.mu.Unlock()
		return
	}
	s.logf("client connected  id=%s  addr=%s", client.id, conn.RemoteAddr())

	// Cleanup on disconnect.
	defer func() {
		s.mu.Lock()
		delete(s.clients, client.id)
		client.mu.Lock()
		ownedTunnels := append([]string{}, client.tunnels...)
		client.mu.Unlock()
		s.mu.Unlock()

		for _, tid := range ownedTunnels {
			s.removeTunnel(tid)
		}
		s.logf("client disconnected  id=%s", client.id)
	}()

	go s.keepalive(client)

	// Command read loop.
	for {
		msg, err := proto.ReadMessage(conn)
		if err != nil {
			return
		}
		switch msg.Type {
		case proto.TypeAddTunnel:
			var info proto.TunnelInfo
			if err := proto.Decode(msg, &info); err == nil {
				go s.handleAddTunnel(client, info)
			}
		case proto.TypeDeleteTunnel:
			var p proto.DeleteTunnelPayload
			if err := proto.Decode(msg, &p); err == nil {
				go s.handleDeleteTunnel(client, p.TunnelID)
			}
		case proto.TypeListTunnels:
			go s.handleListTunnels(client)
		case proto.TypePong:
			// keepalive acknowledged — nothing to do
		}
	}
}

// keepalive sends pings to the client periodically.
func (s *Server) keepalive(client *clientConn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := client.send(proto.TypePing, nil); err != nil {
			return
		}
	}
}
