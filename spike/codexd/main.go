// Command codexd is a spike (#263): a passive observer of the Codex shared
// app-server daemon. It never answers server requests (approvals etc.).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Deltas are high-volume and carry nothing Ember displays.
var optOut = []string{
	"item/agentMessage/delta", "item/plan/delta", "item/reasoning/summaryTextDelta",
	"item/reasoning/summaryPartAdded", "item/reasoning/textDelta",
	"item/commandExecution/outputDelta", "item/fileChange/outputDelta",
	"command/exec/outputDelta", "process/outputDelta", "turn/diff/updated",
	"item/mcpToolCall/progress", "fuzzyFileSearch/sessionUpdated",
}

type msg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type client struct {
	ws        *wsConn
	next      atomic.Int64
	mu        sync.Mutex
	pending   map[int64]chan msg
	verbose   bool
	subscribe bool
	lazy      bool
	resumed   sync.Map
	counts    sync.Map
}

func (c *client) call(method string, params any) (msg, error) {
	id := c.next.Add(1)
	ch := make(chan msg, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := c.ws.WriteText(b); err != nil {
		return msg{}, err
	}
	select {
	case m := <-ch:
		return m, nil
	case <-time.After(30 * time.Second):
		return msg{}, fmt.Errorf("%s: timeout", method)
	}
}

func (c *client) notify(method string) error {
	b, _ := json.Marshal(map[string]any{"method": method})
	return c.ws.WriteText(b)
}

func trunc(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + fmt.Sprintf("…(%dB)", len(b))
	}
	return string(b)
}

func (c *client) readLoop() {
	for {
		raw, err := c.ws.ReadMessage()
		if err != nil {
			log.Fatalf("read: %v", err)
		}
		var m msg
		if err := json.Unmarshal(raw, &m); err != nil {
			log.Printf("bad json: %s", trunc(raw, 200))
			continue
		}
		switch {
		case m.Method == "" && m.ID != nil: // response
			var id int64
			json.Unmarshal(m.ID, &id)
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case m.Method != "" && m.ID != nil: // server request: NEVER answer
			c.count("REQ " + m.Method)
			log.Printf("SERVER-REQUEST (unanswered) id=%s %s %s", m.ID, m.Method, trunc(m.Params, 400))
		default:
			c.count(m.Method)
			c.onNotification(m, len(raw))
		}
	}
}

func (c *client) count(k string) {
	v, _ := c.counts.LoadOrStore(k, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

func (c *client) onNotification(m msg, size int) {
	var p map[string]any
	json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "thread/status/changed":
		id, _ := p["threadId"].(string)
		st, _ := p["status"].(map[string]any)
		if id == "" || !c.subscribe {
			break
		}
		if c.lazy && st["type"] != "active" {
			// Lazy mode: hold a subscription only while a turn runs, so an
			// exited TUI's thread can unload (60 s) and thread/closed arrives.
			go c.unsubscribe(id)
		} else {
			go c.resume(id)
		}
	case "thread/started":
		if t, ok := p["thread"].(map[string]any); ok {
			if id, _ := t["id"].(string); id != "" && c.subscribe && t["ephemeral"] != true {
				go c.resume(id)
			}
		}
	}
	if c.verbose || m.Method == "thread/status/changed" || m.Method == "turn/started" || m.Method == "turn/completed" ||
		m.Method == "thread/started" || m.Method == "thread/closed" || m.Method == "error" || m.Method == "serverRequest/resolved" {
		log.Printf("N %s (%dB) %s", m.Method, size, trunc(m.Params, 500))
	}
}

func (c *client) unsubscribe(id string) {
	if _, ok := c.resumed.LoadAndDelete(id); !ok {
		return
	}
	r, err := c.call("thread/unsubscribe", map[string]any{"threadId": id})
	if err != nil {
		log.Printf("unsubscribe %s: %v", id, err)
		return
	}
	log.Printf("UNSUBSCRIBED %s %s %s", id, trunc(r.Result, 200), trunc(r.Error, 200))
}

func (c *client) resume(id string) {
	if _, loaded := c.resumed.LoadOrStore(id, true); loaded {
		return
	}
	r, err := c.call("thread/resume", map[string]any{"threadId": id, "excludeTurns": true})
	if err != nil {
		log.Printf("resume %s: %v", id, err)
		return
	}
	if r.Error != nil {
		log.Printf("resume %s error: %s", id, r.Error)
		c.resumed.Delete(id) // not persisted yet: retry on the next status change
		return
	}
	var res struct {
		Thread map[string]any `json:"thread"`
	}
	json.Unmarshal(r.Result, &res)
	log.Printf("RESUMED %s cwd=%v source=%v status=%v (%dB result)", id, res.Thread["cwd"], res.Thread["source"], res.Thread["status"], len(r.Result))
}

func main() {
	home, _ := os.UserHomeDir()
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	sock := flag.String("sock", filepath.Join(codexHome, "app-server-control", "app-server-control.sock"), "control socket")
	verbose := flag.Bool("v", false, "log every notification")
	subscribe := flag.Bool("subscribe", true, "thread/resume loaded threads")
	lazy := flag.Bool("lazy", false, "subscribe only while a thread is active")
	// "codex_app_server_daemon" is in the server's NON_ORIGINATING_CLIENT_NAMES:
	// any other name, if it is the first to initialize, becomes the daemon's
	// process-global originator (rollouts, request headers) for every session.
	name := flag.String("name", "codex_app_server_daemon", "clientInfo.name")
	stats := flag.Duration("stats", 0, "print message counts every interval")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ws, err := dialWS(*sock)
	if err != nil {
		log.Fatal(err)
	}
	c := &client{ws: ws, pending: map[int64]chan msg{}, verbose: *verbose, subscribe: *subscribe, lazy: *lazy}
	go c.readLoop()
	r, err := c.call("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": *name, "title": "Ember (passive observer)", "version": "0.0.0"},
		"capabilities": map[string]any{"optOutNotificationMethods": optOut},
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("initialize: %s", trunc(r.Result, 400))
	c.notify("initialized")

	r, _ = c.call("thread/loaded/list", map[string]any{})
	log.Printf("thread/loaded/list: %s", trunc(r.Result, 600))
	var ll struct {
		Data []string `json:"data"`
	}
	json.Unmarshal(r.Result, &ll)
	for _, id := range ll.Data {
		rr, _ := c.call("thread/read", map[string]any{"threadId": id})
		log.Printf("thread/read %s: %s", id, trunc(rr.Result, 400))
		if *subscribe && !*lazy {
			c.resume(id)
		}
	}
	if *stats > 0 {
		go func() {
			for range time.Tick(*stats) {
				out := map[string]int64{}
				c.counts.Range(func(k, v any) bool { out[k.(string)] = v.(*atomic.Int64).Load(); return true })
				b, _ := json.Marshal(out)
				log.Printf("COUNTS %s", b)
			}
		}()
	}
	select {}
}
