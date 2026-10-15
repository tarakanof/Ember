package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

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

// No response shape on purpose: on the shared daemon any response counts as the user's decision.
type outbound struct {
	ID     int64  `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

const lateReplyTTL = 10 * time.Minute

var errConnLost = errors.New("connection lost")

type lateHook struct {
	fn func(error)
	at time.Time
}

type rpcConn struct {
	ws       *wsConn
	next     atomic.Int64
	lastRead atomic.Int64
	done     chan struct{}

	mu      sync.Mutex
	pending map[int64]chan inbound
	late    map[int64]lateHook
	err     error
}

func newRPCConn(ws *wsConn) *rpcConn {
	return &rpcConn{ws: ws, done: make(chan struct{}), pending: map[int64]chan inbound{}, late: map[int64]lateHook{}}
}

func (c *rpcConn) send(m outbound) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.ws.WriteText(b)
}

func (c *rpcConn) notify(method string) error { return c.send(outbound{Method: method}) }

func (c *rpcConn) call(ctx context.Context, method string, params, out any) error {
	return c.callLate(ctx, method, params, out, nil)
}

func (c *rpcConn) callLate(ctx context.Context, method string, params, out any, onLate func(error)) error {
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
		return fmt.Errorf("%s: %w: %w", method, errConnLost, err)
	}
	select {
	case m := <-ch:
		return decodeReply(method, m, out)
	case <-c.done:
		return fmt.Errorf("%s: %w: %w", method, errConnLost, c.closeErr())
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		select {
		case m := <-ch:
			c.mu.Unlock()
			return decodeReply(method, m, out)
		default:
		}
		if onLate != nil {
			c.pruneLateLocked(time.Now())
			c.late[id] = lateHook{fn: onLate, at: time.Now()}
		}
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func (c *rpcConn) pruneLateLocked(now time.Time) {
	for id, h := range c.late {
		if now.Sub(h.at) > lateReplyTTL {
			delete(c.late, id)
		}
	}
}

func decodeReply(method string, m inbound, out any) error {
	if m.Error != nil {
		return fmt.Errorf("%s: %w", method, m.Error)
	}
	if out != nil && len(m.Result) > 0 {
		if err := json.Unmarshal(m.Result, out); err != nil {
			return fmt.Errorf("%s: decode: %w", method, err)
		}
	}
	return nil
}

func (c *rpcConn) lastReadAt() time.Time { return time.Unix(0, c.lastRead.Load()) }

func (c *rpcConn) closeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		return errors.New("connection closed")
	}
	return c.err
}

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
		c.lastRead.Store(time.Now().UnixNano())
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
			if ch := c.pending[id]; ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
			hook, late := c.late[id]
			delete(c.late, id)
			c.mu.Unlock()
			if late {
				hook.fn(decodeReply("", m, nil))
			}
		case m.Method != "" && len(m.ID) > 0:
			// Server requests are never answered: see outbound.
		case m.Method != "":
			onNotify(m.Method, m.Params)
		}
	}
}
