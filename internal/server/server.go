// Package server implements the tailway server side.
// It manages client connections, tunnel registrations, and proxying.
package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Server is the root object that owns all state.
type Server struct {
	key         string
	controlPort int
	dataPort    int
	debug       bool
	tlsCert     *tls.Certificate // self-signed cert for HTTPS tunnels

	totalBytes int64 // atomic — total bytes proxied across all tunnels

	// All maps are guarded by mu.
	mu      sync.RWMutex
	clients map[string]*clientConn
	tunnels map[string]*tunnel
	pending map[string]*pendingConn
}

// TunnelStats holds per-protocol tunnel counts.
type TunnelStats struct {
	TCP    int
	UDP    int
	HTTP   int
	HTTPS  int
	QUIC   int
	SOCKS5 int
}

// New creates a Server with the given key and control port.
// The data port is always controlPort + 1.
func New(key string, controlPort int, debug bool) *Server {
	cert, err := generateSelfSignedCert()
	if err != nil {
		log.Printf("warning: failed to generate TLS cert: %v", err)
	}
	return &Server{
		key:         key,
		controlPort: controlPort,
		dataPort:    controlPort + 1,
		debug:       debug,
		tlsCert:     cert,
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

// TunnelCounts returns per-protocol tunnel counts.
func (s *Server) TunnelCounts() TunnelStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var st TunnelStats
	for _, t := range s.tunnels {
		switch t.info.Protocol {
		case "tcp":
			st.TCP++
		case "udp":
			st.UDP++
		case "http":
			st.HTTP++
		case "https":
			st.HTTPS++
		case "quic":
			st.QUIC++
		case "socks5":
			st.SOCKS5++
		}
	}
	return st
}

// TotalBytes returns the total number of bytes proxied so far.
func (s *Server) TotalBytes() int64 { return atomic.LoadInt64(&s.totalBytes) }

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

// generateSelfSignedCert creates an ECDSA P-256 self-signed certificate
// valid for 10 years, used for HTTPS tunnel TLS termination.
func generateSelfSignedCert() (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tailway"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}
