package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dt/awtrix-ai-status/internal/render"
)

// sampleBaseSession is shown when /state is unreachable or empty. Values are
// representative; previewSession fills in any enabled-but-missing fields.
func sampleBaseSession() render.Session {
	return render.Session{
		Source: "mbp", Tool: "claude", Session: "sample", State: "running",
	}
}

// fetchBaseSession GETs <serverURL>/state and returns the priority-winning
// session for the preview plus live=true. On any error, timeout, non-200, or
// empty session list it returns sampleBaseSession() and live=false.
func fetchBaseSession(serverURL string, timeout time.Duration) (render.Session, bool) {
	if serverURL == "" {
		return sampleBaseSession(), false
	}
	url := strings.TrimRight(serverURL, "/") + "/state"

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return sampleBaseSession(), false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return sampleBaseSession(), false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return sampleBaseSession(), false
	}

	var snap render.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return sampleBaseSession(), false
	}
	win, _, _ := render.PickWinning(snap.Sessions)
	if win == nil {
		return sampleBaseSession(), false
	}
	return *win, true
}
