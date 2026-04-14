// Package server implements the tailway server side.
// It manages client connections, tunnel registrations, and proxying.
package server

import (
	"fmt"
	"log"
	"net"
	"sync"
)

// Server is the root object that owns all state.
type Server struct {
	key         string
	controlPort int
	dataPort    int
	debug       bool

	// All maps are guarded by mu.
	mu      sync.RWMutex
	clients map[string]*clientConn
	tunnels map[string]*tunnel
	pending map[string]*pendingConn
}

// New creates a Server with the given key and control port.
// The data port is always controlPort + 1.
func New(key string, controlPort int, debug bool) *Server {
	return &Server{
		key:         key,
		controlPort: controlPort,
		dataPort:    controlPort + 1,
		debug:       debug,
		clients:     make(map[string]*clientConn),
		tunnels:     make(map[string]*tunnel),
		pending:     make(map[string]*pendingConn),
	}
}

// logf logs only when debug mode is enabled.
func (s *Server) logf(format string, args ...any) {
	if s.debug {
		log.Printf(format, args...)
	}
}

// Run starts both listeners and blocks until the control listener fails.
func (s *Server) Run() error {
	controlLn, err := net.Listen("tcp", fmt.Sprintf(":%d", s.controlPort))
	if err != nil {
		return fmt.Errorf("control port :%d: %w", s.controlPort, err)
	}
	dataLn, err := net.Listen("tcp", fmt.Sprintf(":%d", s.dataPort))
	if err != nil {
		controlLn.Close()
		return fmt.Errorf("data port :%d: %w", s.dataPort, err)
	}

	s.logf("listening  control=:%d  data=:%d", s.controlPort, s.dataPort)

	go s.acceptData(dataLn)
	go s.sweepPending()

	return s.acceptControl(controlLn)
}

// Start binds listeners and runs the server in background goroutines.
// Returns immediately after listeners are bound. errCh receives a value
// if the server encounters a fatal error later.
func (s *Server) Start() (<-chan error, error) {
	controlLn, err := net.Listen("tcp", fmt.Sprintf(":%d", s.controlPort))
	if err != nil {
		return nil, fmt.Errorf("control port :%d: %w", s.controlPort, err)
	}
	dataLn, err := net.Listen("tcp", fmt.Sprintf(":%d", s.dataPort))
	if err != nil {
		controlLn.Close()
		return nil, fmt.Errorf("data port :%d: %w", s.dataPort, err)
	}
	s.logf("listening  control=:%d  data=:%d", s.controlPort, s.dataPort)
	ch := make(chan error, 1)
	go func() {
		go s.acceptData(dataLn)
		go s.sweepPending()
		ch <- s.acceptControl(controlLn)
	}()
	return ch, nil
}

// ClientCount returns the number of currently connected clients.
func (s *Server) ClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// TunnelCounts returns the number of open TCP and UDP tunnels.
func (s *Server) TunnelCounts() (tcp, udp int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tunnels {
		if t.info.Protocol == "udp" {
			udp++
		} else {
			tcp++
		}
	}
	return
}

// ControlPort returns the server's control port.
func (s *Server) ControlPort() int { return s.controlPort }

// DataPort returns the server's data port.
func (s *Server) DataPort() int { return s.dataPort }

func (s *Server) acceptControl(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleControl(conn)
	}
}

func (s *Server) acceptData(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleData(conn)
	}
}
