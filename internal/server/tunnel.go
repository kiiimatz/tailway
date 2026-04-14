package server

import (
	"fmt"
	"net"

	"github.com/google/uuid"
	"github.com/kiiimatz/tailway/internal/proto"
)

// tunnel holds the server-side state for one registered tunnel.
type tunnel struct {
	info     proto.TunnelInfo
	clientID string
	listener net.Listener  // TCP tunnels only
	udpConn  *net.UDPConn  // UDP tunnels only
	done     chan struct{}  // closed when the tunnel is removed
}

// handleAddTunnel validates and creates a new tunnel for the given client.
func (s *Server) handleAddTunnel(client *clientConn, info proto.TunnelInfo) {
	if info.ID == "" {
		info.ID = uuid.New().String()
	}

	// Check for port conflicts before opening any socket.
	s.mu.Lock()
	for _, t := range s.tunnels {
		if t.info.ServerPort == info.ServerPort && t.info.Protocol == info.Protocol {
			s.mu.Unlock()
			client.send(proto.TypeAddTunnelError, proto.AddTunnelErrorPayload{
				TunnelID: info.ID,
				Error:    fmt.Sprintf("port %d/%s already in use", info.ServerPort, info.Protocol),
			})
			return
		}
	}
	s.mu.Unlock()

	switch info.Protocol {
	case "tcp":
		s.addTCPTunnel(client, info)
	case "udp":
		s.addUDPTunnel(client, info)
	default:
		client.send(proto.TypeAddTunnelError, proto.AddTunnelErrorPayload{
			TunnelID: info.ID,
			Error:    "unsupported protocol: " + info.Protocol,
		})
	}
}

func (s *Server) addTCPTunnel(client *clientConn, info proto.TunnelInfo) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", info.ServerPort))
	if err != nil {
		client.send(proto.TypeAddTunnelError, proto.AddTunnelErrorPayload{
			TunnelID: info.ID, Error: err.Error(),
		})
		return
	}

	t := &tunnel{info: info, clientID: client.id, listener: ln, done: make(chan struct{})}
	s.registerTunnel(client, t)
	client.send(proto.TypeTunnelAdded, proto.TunnelAddedPayload{TunnelID: info.ID})
	s.logf("tunnel added  id=%s  proto=tcp  port=%d", info.ID, info.ServerPort)
	go s.serveTCP(t, client)
}

func (s *Server) addUDPTunnel(client *clientConn, info proto.TunnelInfo) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", info.ServerPort))
	if err != nil {
		client.send(proto.TypeAddTunnelError, proto.AddTunnelErrorPayload{
			TunnelID: info.ID, Error: err.Error(),
		})
		return
	}
	uc, err := net.ListenUDP("udp", addr)
	if err != nil {
		client.send(proto.TypeAddTunnelError, proto.AddTunnelErrorPayload{
			TunnelID: info.ID, Error: err.Error(),
		})
		return
	}

	t := &tunnel{info: info, clientID: client.id, udpConn: uc, done: make(chan struct{})}
	s.registerTunnel(client, t)
	client.send(proto.TypeTunnelAdded, proto.TunnelAddedPayload{TunnelID: info.ID})
	s.logf("tunnel added  id=%s  proto=udp  port=%d", info.ID, info.ServerPort)
	go s.serveUDP(t, client)
}

// registerTunnel adds a tunnel to the server map and the client's owned list.
func (s *Server) registerTunnel(client *clientConn, t *tunnel) {
	s.mu.Lock()
	s.tunnels[t.info.ID] = t
	client.mu.Lock()
	client.tunnels = append(client.tunnels, t.info.ID)
	client.mu.Unlock()
	s.mu.Unlock()
}

// handleDeleteTunnel removes a tunnel owned by the given client.
func (s *Server) handleDeleteTunnel(client *clientConn, tunnelID string) {
	s.mu.RLock()
	t, ok := s.tunnels[tunnelID]
	s.mu.RUnlock()

	if !ok || t.clientID != client.id {
		return
	}

	s.removeTunnel(tunnelID)

	client.mu.Lock()
	filtered := client.tunnels[:0]
	for _, tid := range client.tunnels {
		if tid != tunnelID {
			filtered = append(filtered, tid)
		}
	}
	client.tunnels = filtered
	client.mu.Unlock()

	client.send(proto.TypeTunnelDeleted, proto.TunnelDeletedPayload{TunnelID: tunnelID})
}

// handleListTunnels sends the client a snapshot of its own tunnels.
func (s *Server) handleListTunnels(client *clientConn) {
	client.mu.Lock()
	ids := append([]string{}, client.tunnels...)
	client.mu.Unlock()

	s.mu.RLock()
	infos := make([]proto.TunnelInfo, 0, len(ids))
	for _, id := range ids {
		if t, ok := s.tunnels[id]; ok {
			infos = append(infos, t.info)
		}
	}
	s.mu.RUnlock()

	client.send(proto.TypeTunnelList, proto.TunnelListPayload{Tunnels: infos})
}

// removeTunnel stops the tunnel's listener and removes it from the server map.
func (s *Server) removeTunnel(tunnelID string) {
	s.mu.Lock()
	t, ok := s.tunnels[tunnelID]
	if ok {
		delete(s.tunnels, tunnelID)
	}
	s.mu.Unlock()

	if !ok {
		return
	}
	close(t.done)
	if t.listener != nil {
		t.listener.Close()
	}
	if t.udpConn != nil {
		t.udpConn.Close()
	}
	s.logf("tunnel removed  id=%s", tunnelID)
}
