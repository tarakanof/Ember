package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/tarakanof/ember/internal/nowplaying"
)

// plexClientID names Ember to Plex as the controller (X-Plex-Client-Identifier).
const plexClientID = "ember-nowplaying"

// plexControl is the Plex state control requests share with the poller:
// each player's machine identifier (the command target) and the last volume
// read or set per target. Plex's sessions list carries no volume; the
// player's timeline does.
type plexControl struct {
	mu      sync.Mutex
	targets map[string]string // player title → machineIdentifier
	volumes map[string]int    // machineIdentifier → 0-100
	volMu   sync.Mutex        // one volume read-modify-write at a time
	cmdID   atomic.Int64
}

func (c *plexControl) setTarget(player, target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.targets) >= 16 {
		clear(c.targets)
	}
	c.targets[player] = target
}

func (c *plexControl) target(player string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.targets[player]
}

// volume returns the target's last known volume, nil when unknown.
func (c *plexControl) volume(target string) *int {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.volumes[target]
	if !ok {
		return nil
	}
	return &v
}

func (c *plexControl) setVolume(target string, v int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.volumes) >= 16 {
		clear(c.volumes)
	}
	c.volumes[target] = v
}

// control sends req to the Plex player of e through the server's player
// proxy (/player/playback/*, X-Plex-Target-Client-Identifier).
func (p *plexSource) control(ctx context.Context, e nowplaying.Entry, req controlRequest) (controlResult, error) {
	target := p.ctl.target(e.Player)
	if target == "" {
		return controlResult{}, errNoController
	}
	res := controlResult{Status: "done", Source: plexSourceID}
	var err error
	switch req.Action {
	case actPlayPause:
		cmd := "play"
		if e.State == nowplaying.Playing {
			cmd = "pause"
		}
		err = p.command(ctx, target, cmd, nil)
	case actNext:
		err = p.command(ctx, target, "skipNext", nil)
	case actPrevious:
		err = p.command(ctx, target, "skipPrevious", nil)
	case actVolume:
		var v int
		v, err = p.stepVolume(ctx, target, req.Delta)
		res.Volume = &v
	}
	if err != nil {
		return controlResult{}, fmt.Errorf("%w: %w", errControlFailed, err)
	}
	p.wake() // the next poll shows the result
	return res, nil
}

// stepVolume moves the target's volume by delta from the last known level,
// read from its timeline first when not known: a step from a guessed level
// could blast.
func (p *plexSource) stepVolume(ctx context.Context, target string, delta int) (int, error) {
	p.ctl.volMu.Lock()
	defer p.ctl.volMu.Unlock()
	cur := p.ctl.volume(target)
	if cur == nil {
		v, err := p.timelineVolume(ctx, target)
		if err != nil {
			return 0, err
		}
		cur = &v
	}
	v := max(0, min(100, *cur+delta))
	if err := p.command(ctx, target, "setParameters", url.Values{"volume": {strconv.Itoa(v)}}); err != nil {
		return 0, err
	}
	p.ctl.setVolume(target, v)
	return v, nil
}

func (p *plexSource) playerRequest(ctx context.Context, target, path string, q url.Values) (*http.Response, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("type", "music")
	q.Set("commandID", strconv.FormatInt(p.ctl.cmdID.Add(1), 10))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.URL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header = p.headers()
	req.Header.Del("Accept") // player answers are XML
	req.Header.Set("X-Plex-Target-Client-Identifier", target)
	req.Header.Set("X-Plex-Client-Identifier", plexClientID)
	return p.client.Do(req)
}

func (p *plexSource) command(ctx context.Context, target, cmd string, q url.Values) error {
	resp, err := p.playerRequest(ctx, target, "/player/playback/"+cmd, q)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plex %s: HTTP %d", cmd, resp.StatusCode)
	}
	return nil
}

// timelineVolume reads the player's music timeline volume.
func (p *plexSource) timelineVolume(ctx context.Context, target string) (int, error) {
	resp, err := p.playerRequest(ctx, target, "/player/timeline/poll", url.Values{"wait": {"0"}})
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("plex timeline: HTTP %d", resp.StatusCode)
	}
	var mc struct {
		Timelines []struct {
			Type   string `xml:"type,attr"`
			Volume string `xml:"volume,attr"`
		} `xml:"Timeline"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&mc); err != nil {
		return 0, fmt.Errorf("plex timeline: %w", err)
	}
	for _, t := range mc.Timelines {
		if t.Type != "music" || t.Volume == "" {
			continue
		}
		v, err := strconv.Atoi(t.Volume)
		if err != nil || v < 0 || v > 100 {
			return 0, fmt.Errorf("plex timeline: volume %q", t.Volume)
		}
		p.ctl.setVolume(target, v)
		return v, nil
	}
	return 0, fmt.Errorf("plex timeline: no music volume")
}
