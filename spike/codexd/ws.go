package main

// Minimal RFC 6455 client over a Unix socket (stdlib only). The Codex
// app-server control socket speaks WebSocket (handshake URL ws://localhost/rpc),
// one JSON-RPC message per text frame.

import (
	"bufio"
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
)

type wsConn struct {
	c  net.Conn
	br *bufio.Reader
	mu sync.Mutex
}

func dialWS(sock string) (*wsConn, error) {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	kb := make([]byte, 16)
	rand.Read(kb)
	key := base64.StdEncoding.EncodeToString(kb)
	fmt.Fprintf(c, "GET /rpc HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", key)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.StatusCode != 101 || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(h[:]) {
		c.Close()
		return nil, fmt.Errorf("ws handshake: %s", resp.Status)
	}
	return &wsConn{c: c, br: br}, nil
}

func (w *wsConn) writeFrame(op byte, p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	hdr := []byte{0x80 | op}
	n := len(p)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n < 65536:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	var mask [4]byte
	rand.Read(mask[:])
	hdr = append(hdr, mask[:]...)
	buf := make([]byte, n)
	for i := range p {
		buf[i] = p[i] ^ mask[i%4]
	}
	_, err := w.c.Write(append(hdr, buf...))
	return err
}

func (w *wsConn) WriteText(p []byte) error { return w.writeFrame(1, p) }

// ReadMessage returns the next complete text/binary message, answering pings.
func (w *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
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
		if h[1]&0x80 != 0 {
			return nil, errors.New("masked server frame")
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.br, p); err != nil {
			return nil, err
		}
		switch op {
		case 9:
			w.writeFrame(10, p)
			continue
		case 10:
			continue
		case 8:
			return nil, io.EOF
		}
		msg = append(msg, p...)
		if fin {
			return msg, nil
		}
	}
}
