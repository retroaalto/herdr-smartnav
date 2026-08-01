// Package herdr is a minimal newline-delimited JSON-RPC 2.0 client for the
// herdr unix socket API plus a long-lived event stream reader.
package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Client is a one-shot-per-call JSON-RPC client. Each Call opens a fresh
// connection (the herdr server does not require persistent request connections,
// and short-lived calls avoid stream interleaving with the event subscriber).
type Client struct {
	SocketPath string
	Timeout    time.Duration
	idCounter  atomic.Uint64
}

func NewClient() *Client {
	sp := os.Getenv("HERDR_SOCKET_PATH")
	if sp == "" {
		if home := os.Getenv("HOME"); home != "" {
			sp = filepath.Join(home, ".config", "herdr", "herdr.sock")
		}
	}
	return &Client{SocketPath: sp, Timeout: 6 * time.Second}
}

// rpcEnvelope is the wire format. id MUST be a string (server rejects integers).
type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Call invokes method with params (params may be nil for no-params methods that
// still require a params object — pass a non-nil empty map in that case).
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	if c.SocketPath == "" {
		return nil, errors.New("herdr socket path not set (set HERDR_SOCKET_PATH)")
	}
	var p json.RawMessage
	if params == nil {
		// many herdr methods require a `params` field; send {} when caller passes nil.
		p = json.RawMessage("{}")
	} else {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		p = b
	}
	id := c.idCounter.Add(1)
	req := rpcEnvelope{JSONRPC: "2.0", ID: fmt.Sprintf("%d", id), Method: method, Params: p}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')

	conn, err := net.Dial("unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", c.SocketPath, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(c.Timeout))

	if _, err := conn.Write(body); err != nil {
		return nil, err
	}

	rd := bufio.NewReader(conn)
	line, err := rd.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp rpcEnvelope
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode: %w (line=%q)", err, string(line))
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// Event is a pushed server event. The server uses snake_case type strings in
// the payload (data.type) even though subscriptions are requested with dotted
// names (pane.focused).
type Event struct {
	Event string          `json:"event"` // top-level, e.g. "pane_focused"
	Data  json.RawMessage `json:"data"`
}

// Subscribe opens a persistent connection to the event stream and calls handler
// for each event line until the connection closes or handler returns false.
// Subscriptions are dotted: "pane.focused", "layout.updated", etc.
func Subscribe(socketPath string, subscriptions []string, handler func(Event) bool) error {
	subs := make([]map[string]string, len(subscriptions))
	for i, s := range subscriptions {
		subs[i] = map[string]string{"type": s}
	}
	params := struct {
		Subscriptions []map[string]string `json:"subscriptions"`
	}{Subscriptions: subs}
	pb, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal subscribe params: %w", err)
	}
	req := rpcEnvelope{JSONRPC: "2.0", ID: "sub1", Method: "events.subscribe", Params: pb}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal subscribe request: %w", err)
	}
	body = append(body, '\n')

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socketPath, err)
	}
	defer conn.Close()

	if _, err := conn.Write(body); err != nil {
		return err
	}
	rd := bufio.NewReader(conn)
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			return err
		}
		line = trimTrailingNL(line)
		if len(line) == 0 {
			continue
		}
		var resp rpcEnvelope
		if json.Unmarshal(line, &resp) == nil && resp.Error != nil {
			return resp.Error
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Event == "" {
			continue
		}
		if !handler(ev) {
			return nil
		}
	}
}

func trimTrailingNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
