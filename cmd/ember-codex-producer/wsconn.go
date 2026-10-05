package main

// A minimal RFC 6455 WebSocket over a Unix socket, stdlib only. The Codex
// app-server control socket speaks WebSocket (handshake GET ws://localhost/rpc)
// with one JSON-RPC message per text frame.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	// wsMaxMessage bounds one inbound message; thread/read and resume
	// without turns are a few KB.
	wsMaxMessage = 16 << 20

	opCont  = 0
	opText  = 1
	opClose = 8
	opPing  = 9
	opPong  = 10
)

// wsWriteTimeout bounds one frame write, so a daemon that stops reading
// fails the session instead of blocking it.
const wsWriteTimeout = 10 * time.Second

type wsConn struct {
	c      net.Conn
	br     *bufio.Reader
	client bool   // a client masks its frames; a server must not
	max    uint64 // message size limit; 0 means wsMaxMessage
	// writeTimeout bounds one frame write; 0 means wsWriteTimeout.
	writeTimeout time.Duration
	mu           sync.Mutex // serializes frame writes
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// dialWS opens the Unix socket at path and performs the client handshake.
func dialWS(ctx context.Context, path string) (*wsConn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	kb := make([]byte, 16)
	_, _ = rand.Read(kb)
	key := base64.StdEncoding.EncodeToString(kb)
	if _, err := fmt.Fprintf(c, "GET /rpc HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", key); err != nil {
		_ = c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("websocket handshake: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(key) {
		_ = c.Close()
		return nil, fmt.Errorf("websocket handshake: %s", resp.Status)
	}
	_ = c.SetDeadline(time.Time{})
	return &wsConn{c: c, br: br, client: true}, nil
}

func (w *wsConn) Close() error { return w.c.Close() }

func (w *wsConn) writeFrame(op byte, p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	hdr := []byte{0x80 | op}
	var maskBit byte
	if w.client {
		maskBit = 0x80
	}
	n := len(p)
	switch {
	case n < 126:
		hdr = append(hdr, maskBit|byte(n))
	case n < 65536:
		hdr = append(hdr, maskBit|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, maskBit|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	buf := make([]byte, 0, len(hdr)+4+n)
	buf = append(buf, hdr...)
	if w.client {
		var mask [4]byte
		_, _ = rand.Read(mask[:])
		buf = append(buf, mask[:]...)
		for i := range p {
			buf = append(buf, p[i]^mask[i%4])
		}
	} else {
		buf = append(buf, p...)
	}
	wt := w.writeTimeout
	if wt == 0 {
		wt = wsWriteTimeout
	}
	_ = w.c.SetWriteDeadline(time.Now().Add(wt))
	_, err := w.c.Write(buf)
	return err
}

// WriteText sends one text message.
func (w *wsConn) WriteText(p []byte) error { return w.writeFrame(opText, p) }

// ReadMessage returns the next complete data message. It answers pings with a
// pong (a WebSocket control frame, not a JSON-RPC message), echoes a close
// frame and reports it as io.EOF, and drains and skips a message larger than
// wsMaxMessage instead of failing the connection.
func (w *wsConn) ReadMessage() ([]byte, error) {
	limit := w.max
	if limit == 0 {
		limit = wsMaxMessage
	}
	var msg []byte
	inMessage, skipping := false, false
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		masked := h[1]&0x80 != 0
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if masked == w.client {
			return nil, errors.New("websocket: bad frame masking")
		}
		control := op >= 8
		switch {
		case control && (!fin || n > 125):
			return nil, errors.New("websocket: fragmented or oversized control frame")
		case !control && inMessage && op != opCont:
			return nil, errors.New("websocket: new data frame inside a fragmented message")
		case !control && !inMessage && op == opCont:
			return nil, errors.New("websocket: continuation without a message")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.br, mask[:]); err != nil {
				return nil, err
			}
		}
		if !control && (skipping || n > limit || uint64(len(msg))+n > limit) {
			if _, err := io.CopyN(io.Discard, w.br, int64(n)); err != nil {
				return nil, err
			}
			msg, inMessage, skipping = nil, !fin, !fin
			continue
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.br, p); err != nil {
			return nil, err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		switch op {
		case opPing:
			if err := w.writeFrame(opPong, p); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			_ = w.writeFrame(opClose, nil)
			return nil, io.EOF
		}
		if !control && op != opCont && op != opText && op != 2 {
			return nil, errors.New("websocket: unknown opcode")
		}
		msg = append(msg, p...)
		inMessage = !fin
		if fin {
			return msg, nil
		}
	}
}
