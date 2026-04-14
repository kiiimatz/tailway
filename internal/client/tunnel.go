package client

import (
	"time"

	"github.com/google/uuid"
	"github.com/kiiimatz/tailway/internal/proto"
)

// EventKind identifies the type of an Event sent to the UI.
type EventKind int

const (
	EventAuthOK EventKind = iota
	EventAuthFail
	EventTunnelAdded
	EventTunnelAddError
	EventTunnelDeleted
	EventTunnelList
	EventDisconnect
)

// Event carries a server-side notification to the UI layer.
type Event struct {
	Kind    EventKind
	Payload any // []*TunnelEntry for tunnel events, string for errors
}

// TunnelEntry holds a tunnel and its client-side start time.
type TunnelEntry struct {
	proto.TunnelInfo
	StartTime time.Time
}

// AddTunnel registers a new tunnel with the server.
func (c *Client) AddTunnel(info proto.TunnelInfo) error {
	info.ID = uuid.New().String()
	c.mu.Lock()
	c.tunnels[info.ID] = &TunnelEntry{TunnelInfo: info}
	c.mu.Unlock()
	return c.send(proto.TypeAddTunnel, info)
}

// DeleteTunnel asks the server to remove the tunnel with the given ID.
func (c *Client) DeleteTunnel(tunnelID string) error {
	return c.send(proto.TypeDeleteTunnel, proto.DeleteTunnelPayload{TunnelID: tunnelID})
}

// ListTunnels asks the server to send the current tunnel list.
func (c *Client) ListTunnels() error {
	return c.send(proto.TypeListTunnels, nil)
}

// snapshot returns a copy of all current TunnelEntry values.
// Caller must hold c.mu (at least read lock).
func (c *Client) snapshot() []*TunnelEntry {
	out := make([]*TunnelEntry, 0, len(c.tunnels))
	for _, e := range c.tunnels {
		cp := *e
		out = append(out, &cp)
	}
	return out
}
