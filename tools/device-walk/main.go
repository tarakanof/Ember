// Command device-walk pushes throwaway preview frames of the Phase-1 display
// rework to the physical clock so a human can judge legibility. Apps are named
// ember-test-* with a short lifetime; run with -clear to remove them early.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

var dumpDir = flag.String("dump", "", "write payload JSON files here instead of POSTing (for curl)")

func push(base, name string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if *dumpDir != "" {
		path := *dumpDir + "/" + name + ".json"
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
		fmt.Printf("%-28s -> %s\n", name, path)
		return nil
	}
	resp, err := http.Post(base+"/api/custom?name="+name, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("%-28s -> ERROR: %v\n", name, err)
		return err
	}
	defer resp.Body.Close()
	fmt.Printf("%-28s -> %s\n", name, resp.Status)
	return nil
}

func clear(base, name string) {
	resp, err := http.Post(base+"/api/custom?name="+name, "application/json", nil)
	if err != nil {
		fmt.Println(name, "clear err:", err)
		return
	}
	resp.Body.Close()
	fmt.Printf("%-28s cleared (%s)\n", name, resp.Status)
}

func main() {
	baseURL := flag.String("base", "http://192.168.0.14", "clock base URL")
	doClear := flag.Bool("clear", false, "clear the test apps and exit")
	flag.Parse()

	names := []string{"ember-test-claude", "ember-test-codex", "ember-test-attn", "ember-test-neutral"}
	if *doClear {
		for _, n := range names {
			clear(*baseURL, n)
		}
		return
	}

	const lifetime = 180 // seconds; expires on its own
	blue := "#3366FF"
	orange := "#FF8800"
	pct50, pct85 := 50, 85
	now := time.Now()

	// 1. Claude, running, source colour blue, source card "MBP", session bar.
	s1 := render.Session{Source: "mbp", Tool: "claude", Session: "t1", State: "running",
		SourceColor: &blue, ContextPct: &pct50, RateWindowPct: &pct85, UpdatedAt: now}
	f1 := render.ComposeFrame(s1, 0 /* cardSource */, []render.Session{s1}, now)
	_ = push(*baseURL, "ember-test-claude", frameApp(&f1, lifetime))

	// 2. Codex, error state (red cursor), source colour orange, source card.
	s2 := render.Session{Source: "studio", Tool: "codex", Session: "t2", State: "error",
		SourceColor: &orange, RateWindowPct: &pct50, UpdatedAt: now}
	f2 := render.ComposeFrame(s2, 0, []render.Session{s2}, now)
	_ = push(*baseURL, "ember-test-codex", frameApp(&f2, lifetime))

	// 3. Attention frame: waiting claude from "mbp" (blinking WAIT MBP).
	s3 := render.Session{Source: "mbp", Tool: "claude", Session: "t3", State: "waiting",
		SourceColor: &blue, UpdatedAt: now}
	snap := render.Snapshot{Now: now, Sessions: []render.Session{s3}}
	if p := render.RenderForCoord(snap, s3.Key(), 0, true, lifetime); p != nil {
		p["lifetime"] = lifetime
		p["duration"] = lifetime
		_ = push(*baseURL, "ember-test-attn", p)
	}

	// 4. No source colour: neutral body + amber waiting eyes.
	s4 := render.Session{Source: "nas", Tool: "claude", Session: "t4", State: "waiting",
		ContextPct: &pct85, UpdatedAt: now}
	f4 := render.ComposeFrame(s4, 0, []render.Session{s4}, now)
	_ = push(*baseURL, "ember-test-neutral", frameApp(&f4, lifetime))

	fmt.Println("\nApps rotate for ~3 min then expire; re-run with -clear to drop them now.")
}

// frameApp wraps a Frame the same way the coordinator publishes one, minus
// prio/force so the test apps don't preempt the live rotation.
func frameApp(f *render.Frame, lifetime int) map[string]any {
	pixels := make([]int, 256)
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			if f.Dirty[y][x] {
				c := f.Pixels[y][x]
				pixels[y*32+x] = (int(c.R) << 16) | (int(c.G) << 8) | int(c.B)
			}
		}
	}
	return map[string]any{
		"draw":     []any{map[string]any{"db": []any{0, 0, 32, 8, pixels}}},
		"lifetime": lifetime,
		"duration": 8,
	}
}
