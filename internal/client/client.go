// Package client implements the tailway client-side networking.
package client

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/kiiimatz/tailway/internal/proto"
)

const (
	controlPort = 7000
	dataPort    = 7001
)

// Client manages a single connection to a tailway server.
type Client struct {
	serverHost string

	controlConn net.Conn
	writeMu     sync.Mutex

	mu      sync.RWMutex
	tunnels map[string]*TunnelEntry

	// Events delivers server-side events to the UI layer.
	Events chan Event
}

// New creates an unconnected Client.
func New() *Client {
	return &Client{
		tunnels: make(map[string]*TunnelEntry),
		Events:  make(chan Event, 64),
	}
}

// Connect dials the server, authenticates with key, then starts the read loop.
func (c *Client) Connect(serverAddr, key string) error {
	host, _, err := net.SplitHostPort(serverAddr)
	if err != nil {
		host = serverAddr // no port supplied — use defaults
	}
	c.serverHost = host

	conn, err := net.DialTimeout("tcp",
		fmt.Sprintf("%s:%d", host, controlPort), 10*time.Second)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", serverAddr, err)
	}

	// Auth handshake.
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := proto.Send(conn, proto.TypeAuth, proto.AuthPayload{Key: key}); err != nil {
		conn.Close()
		return err
	}
	msg, err := proto.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return err
	}
	conn.SetDeadline(time.Time{})

	switch msg.Type {
	case proto.TypeAuthOK:
		// success
	case proto.TypeAuthFail:
		conn.Close()
		var p proto.AuthFailPayload
		proto.Decode(msg, &p)
		return fmt.Errorf("authentication failed: %s", p.Reason)
	default:
		conn.Close()
		return fmt.Errorf("unexpected auth response: %s", msg.Type)
	}

	c.controlConn = conn
	go c.readLoop()
	return nil
}

// ServerHost returns the hostname of the connected server.
func (c *Client) ServerHost() string {
	return c.serverHost
}

// send writes a message on the control connection (thread-safe).
func (c *Client) send(msgType string, payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return proto.Send(c.controlConn, msgType, payload)
}

// readLoop receives server messages and dispatches them.
func (c *Client) readLoop() {
	for {
		msg, err := proto.ReadMessage(c.controlConn)
		if err != nil {
			c.Events <- Event{Kind: EventDisconnect}
			return
		}
		c.dispatch(msg)
	}
}

// dispatch routes a received message to the appropriate handler.
func (c *Client) dispatch(msg proto.Message) {
	switch msg.Type {
	case proto.TypeTunnelAdded:
		var p proto.TunnelAddedPayload
		if err := proto.Decode(msg, &p); err != nil {
			return
		}
		c.mu.Lock()
		if entry, ok := c.tunnels[p.TunnelID]; ok {
			entry.StartTime = time.Now()
		}
		snap := c.snapshot()
		c.mu.Unlock()
		c.Events <- Event{Kind: EventTunnelAdded, Payload: snap}

	case proto.TypeAddTunnelError:
		var p proto.AddTunnelErrorPayload
		if err := proto.Decode(msg, &p); err != nil {
			return
		}
		c.mu.Lock()
		delete(c.tunnels, p.TunnelID)
		c.mu.Unlock()
		c.Events <- Event{Kind: EventTunnelAddError, Payload: p.Error}

	case proto.TypeTunnelDeleted:
		var p proto.TunnelDeletedPayload
		if err := proto.Decode(msg, &p); err != nil {
			return
		}
		c.mu.Lock()
		delete(c.tunnels, p.TunnelID)
		snap := c.snapshot()
		c.mu.Unlock()
		c.Events <- Event{Kind: EventTunnelDeleted, Payload: snap}

	case proto.TypeTunnelList:
		var p proto.TunnelListPayload
		if err := proto.Decode(msg, &p); err != nil {
			return
		}
		c.mu.Lock()
		for _, info := range p.Tunnels {
			if _, exists := c.tunnels[info.ID]; !exists {
				c.tunnels[info.ID] = &TunnelEntry{TunnelInfo: info, StartTime: time.Now()}
			}
		}
		snap := c.snapshot()
		c.mu.Unlock()
		c.Events <- Event{Kind: EventTunnelList, Payload: snap}

	case proto.TypeNewConn:
		var p proto.NewConnPayload
		if err := proto.Decode(msg, &p); err != nil {
			return
		}
		go c.handleNewConn(p) // must not block the read loop

	case proto.TypePing:
		c.send(proto.TypePong, nil)
	}
}
