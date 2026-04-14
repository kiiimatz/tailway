package proto

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	TypeAuth           = "auth"
	TypeAuthOK         = "auth_ok"
	TypeAuthFail       = "auth_fail"
	TypeAddTunnel      = "add_tunnel"
	TypeTunnelAdded    = "tunnel_added"
	TypeAddTunnelError = "add_tunnel_error"
	TypeDeleteTunnel   = "delete_tunnel"
	TypeTunnelDeleted  = "tunnel_deleted"
	TypeListTunnels    = "list_tunnels"
	TypeTunnelList     = "tunnel_list"
	TypeNewConn        = "new_conn"
	TypeDataConn       = "data_conn"
	TypePing           = "ping"
	TypePong           = "pong"
)

// Message is the envelope for all protocol messages.
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Payload types

type AuthPayload struct {
	Key string `json:"key"`
}

type AuthFailPayload struct {
	Reason string `json:"reason"`
}

type TunnelInfo struct {
	ID         string `json:"id"`
	Protocol   string `json:"protocol"` // "tcp" or "udp"
	ClientPort int    `json:"client_port"`
	ServerPort int    `json:"server_port"`
}

type TunnelAddedPayload struct {
	TunnelID string `json:"tunnel_id"`
}

type AddTunnelErrorPayload struct {
	TunnelID string `json:"tunnel_id"`
	Error    string `json:"error"`
}

type DeleteTunnelPayload struct {
	TunnelID string `json:"tunnel_id"`
}

type TunnelDeletedPayload struct {
	TunnelID string `json:"tunnel_id"`
}

type TunnelListPayload struct {
	Tunnels []TunnelInfo `json:"tunnels"`
}

type NewConnPayload struct {
	ConnID     string `json:"conn_id"`
	TunnelID   string `json:"tunnel_id"`
	Protocol   string `json:"protocol"`
	UDPData    []byte `json:"udp_data,omitempty"`
	UDPSrcAddr string `json:"udp_src_addr,omitempty"`
}

type DataConnPayload struct {
	ConnID string `json:"conn_id"`
}

// WriteMessage encodes and sends a length-prefixed JSON message.
func WriteMessage(w io.Writer, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(data) > 1<<24 {
		return fmt.Errorf("message too large: %d bytes", len(data))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// ReadMessage reads a length-prefixed JSON message.
func ReadMessage(r io.Reader) (Message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Message{}, err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length > 1<<24 {
		return Message{}, fmt.Errorf("message too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return Message{}, err
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// Encode creates a Message from a type and payload struct.
func Encode(msgType string, payload any) (Message, error) {
	if payload == nil {
		return Message{Type: msgType}, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Message{}, err
	}
	return Message{Type: msgType, Payload: json.RawMessage(data)}, nil
}

// Decode extracts the payload of a Message into a target struct.
func Decode(msg Message, target any) error {
	if len(msg.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(msg.Payload, target)
}

// Send encodes and writes a message in one step.
func Send(w io.Writer, msgType string, payload any) error {
	msg, err := Encode(msgType, payload)
	if err != nil {
		return err
	}
	return WriteMessage(w, msg)
}

// WriteFrame writes a raw binary frame with a 4-byte big-endian length header.
// Used for UDP packet relay over TCP data connections.
func WriteFrame(w io.Writer, data []byte) error {
	if len(data) > 65535 {
		return fmt.Errorf("frame too large: %d bytes", len(data))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// ReadFrame reads a raw binary frame written by WriteFrame.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > 65535 {
		return nil, fmt.Errorf("frame too large: %d bytes", n)
	}
	data := make([]byte, n)
	_, err := io.ReadFull(r, data)
	return data, err
}
