package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SessionSummary struct {
	Source, Tool, Session, State, Message string
}

type View struct {
	DominantState string
	LastMessage   string
	ActiveCount   int
	Sessions      []SessionSummary
}

func readView(stateDir string, ttl time.Duration) View {
	v := View{}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return v
	}
	cutoff := time.Now().Add(-ttl)
	// Sort for deterministic LastMessage selection
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	// Dominance order per spec: waiting > error > running > done.
	// ActiveCount counts only running+waiting+error (done excluded).
	priority := map[string]int{"waiting": 4, "error": 3, "running": 2, "done": 1}
	active := map[string]bool{"waiting": true, "error": true, "running": true}
	bestRank := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(stateDir, e.Name())
		info, err := os.Stat(path)
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s SessionSummary
		if err := json.Unmarshal(body, &s); err != nil {
			continue
		}
		v.Sessions = append(v.Sessions, s)
		if r := priority[s.State]; r > 0 {
			if active[s.State] {
				v.ActiveCount++
			}
			if r > bestRank {
				bestRank = r
				v.DominantState = s.State
				v.LastMessage = s.Message
			}
		}
	}
	return v
}
