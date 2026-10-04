package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// inbound is any message the app-server sends: a response (id, no method), a
// notification (method, no id) or a server request (method and id).
type inbound struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// outbound is the only message shape this client writes: a request (id from
// rpcConn.next) or a notification (no id). It has no result or error field,
// so the client cannot express a JSON-RPC response, and no id it writes is
// ever taken from a received message. A server request (an approval, a user
// input prompt, an elicitation) is therefore never answered: on the shared
// daemon any response counts as the user's decision.
type outbound struct {
	ID     int64  `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// rpcConn is a JSON-RPC 2.0 client over one WebSocket.
type rpcConn struct {
	ws   *wsConn
	next atomic.Int64
	done chan struct{} // closed when readLoop exits

	mu      sync.Mutex // protects pending and err
	pending map[int64]chan inbound
	err     error
}

func newRPCConn(ws *wsConn) *rpcConn {
	return &rpcConn{ws: ws, done: make(chan struct{}), pending: map[int64]chan inbound{}}
}

func (c *rpcConn) send(m outbound) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.ws.WriteText(b)
}

func (c *rpcConn) notify(method string) error { return c.send(outbound{Method: method}) }

// call sends a request and decodes its result into out (when non-nil).
func (c *rpcConn) call(ctx context.Context, method string, params, out any) error {
	id := c.next.Add(1)
	ch := make(chan inbound, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(outbound{ID: id, Method: method, Params: params}); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return fmt.Errorf("%s: %w", method, m.Error)
		}
		if out != nil && len(m.Result) > 0 {
			if err := json.Unmarshal(m.Result, out); err != nil {
				return fmt.Errorf("%s: decode: %w", method, err)
			}
		}
		return nil
	case <-c.done:
		return fmt.Errorf("%s: %w", method, c.closeErr())
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func (c *rpcConn) closeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		return errors.New("connection closed")
	}
	return c.err
}

// readLoop dispatches responses to their callers and notifications to
// onNotify until the connection fails. Server requests are dropped.
func (c *rpcConn) readLoop(onNotify func(method string, params json.RawMessage)) {
	defer close(c.done)
	for {
		raw, err := c.ws.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return
		}
		var m inbound
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		switch {
		case m.Method == "" && len(m.ID) > 0:
			var id int64
			if json.Unmarshal(m.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		case m.Method != "" && len(m.ID) > 0:
			// A server request: deliberately unanswered (see outbound).
		case m.Method != "":
			onNotify(m.Method, m.Params)
		}
	}
}
