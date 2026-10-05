package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// frame encodes one unmasked server frame.
func frame(fin bool, op byte, p []byte) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	out := []byte{b0}
	switch n := len(p); {
	case n < 126:
		out = append(out, byte(n))
	case n < 65536:
		out = append(out, 126, byte(n>>8), byte(n))
	default:
		out = append(out, 127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}
	return append(out, p...)
}

// pipeClient returns a client wsConn and the raw server end.
func pipeClient(t *testing.T) (*wsConn, net.Conn) {
	t.Helper()
	cli, srv := net.Pipe()
	t.Cleanup(func() { _ = cli.Close(); _ = srv.Close() })
	return &wsConn{c: cli, br: bufio.NewReader(cli), client: true}, srv
}

func serverWrite(t *testing.T, srv net.Conn, frames ...[]byte) {
	t.Helper()
	go func() {
		for _, f := range frames {
			if _, err := srv.Write(f); err != nil {
				return
			}
		}
	}()
}

// readClientFrame decodes one masked client frame from the server end.
func readClientFrame(t *testing.T, srv net.Conn) (op byte, p []byte) {
	t.Helper()
	_ = srv.SetReadDeadline(time.Now().Add(2 * time.Second))
	ws := &wsConn{c: srv, br: bufio.NewReader(srv)}
	var h [2]byte
	if _, err := io.ReadFull(ws.br, h[:]); err != nil {
		t.Fatal(err)
	}
	if h[1]&0x80 == 0 {
		t.Fatal("client frame not masked")
	}
	n := int(h[1] & 0x7f)
	var mask [4]byte
	_, _ = io.ReadFull(ws.br, mask[:])
	p = make([]byte, n)
	_, _ = io.ReadFull(ws.br, p)
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return h[0] & 0x0f, p
}

func TestWS_FragmentedMessageWithPingInBetween(t *testing.T) {
	ws, srv := pipeClient(t)
	serverWrite(t, srv,
		frame(false, opText, []byte(`{"a":`)),
		frame(true, opPing, []byte("hi")),
		frame(false, opCont, []byte(`1`)),
		frame(true, opCont, []byte(`}`)))
	got := make(chan []byte, 1)
	go func() { m, _ := ws.ReadMessage(); got <- m }()
	if op, p := readClientFrame(t, srv); op != opPong || string(p) != "hi" {
		t.Fatalf("want pong hi, got op=%d %q", op, p)
	}
	if m := <-got; string(m) != `{"a":1}` {
		t.Fatalf("message = %q", m)
	}
}

func TestWS_ExtendedLengths(t *testing.T) {
	for _, n := range []int{125, 126, 65535, 65536, 70000} {
		ws, srv := pipeClient(t)
		p := bytes.Repeat([]byte("x"), n)
		serverWrite(t, srv, frame(true, opText, p))
		m, err := ws.ReadMessage()
		if err != nil || len(m) != n {
			t.Fatalf("n=%d: len=%d err=%v", n, len(m), err)
		}
	}
}

func TestWS_CloseIsEchoedAndReportsEOF(t *testing.T) {
	ws, srv := pipeClient(t)
	serverWrite(t, srv, frame(true, opClose, nil))
	errc := make(chan error, 1)
	go func() { _, err := ws.ReadMessage(); errc <- err }()
	if op, _ := readClientFrame(t, srv); op != opClose {
		t.Fatalf("want close echo, got op %d", op)
	}
	if err := <-errc; !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want EOF", err)
	}
}

func TestWS_OversizeMessageIsSkippedNotFatal(t *testing.T) {
	ws, srv := pipeClient(t)
	ws.max = 10
	serverWrite(t, srv,
		frame(true, opText, bytes.Repeat([]byte("a"), 50)), // one big frame
		frame(false, opText, []byte("0123456")),            // fragments that add up too much
		frame(false, opCont, []byte("789abc")),
		frame(true, opCont, []byte("def")),
		frame(true, opText, []byte(`ok`)))
	m, err := ws.ReadMessage()
	if err != nil || string(m) != "ok" {
		t.Fatalf("got %q err=%v, want the next message", m, err)
	}
}

func TestWS_RejectsProtocolViolations(t *testing.T) {
	masked := frame(true, opText, []byte("x"))
	masked[1] |= 0x80
	masked = append(masked[:2], append([]byte{0, 0, 0, 0}, masked[2:]...)...)
	cases := map[string][][]byte{
		"masked server frame":     {masked},
		"fragmented control":      {frame(false, opPing, nil)},
		"oversized control":       {frame(true, opPing, bytes.Repeat([]byte("p"), 126))},
		"text inside fragment":    {frame(false, opText, []byte("a")), frame(true, opText, []byte("b"))},
		"continuation first":      {frame(true, opCont, []byte("a"))},
		"unknown data opcode (3)": {frame(true, 3, []byte("a"))},
	}
	for name, frames := range cases {
		ws, srv := pipeClient(t)
		serverWrite(t, srv, frames...)
		if _, err := ws.ReadMessage(); err == nil || errors.Is(err, io.EOF) {
			t.Errorf("%s: err = %v, want a protocol error", name, err)
		}
	}
}

func TestWS_WriteTimesOutWhenPeerStopsReading(t *testing.T) {
	ws, _ := pipeClient(t) // nobody reads the server end
	ws.writeTimeout = 50 * time.Millisecond
	start := time.Now()
	err := ws.WriteText([]byte(strings.Repeat("x", 10)))
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("write err = %v after %v", err, time.Since(start))
	}
}
