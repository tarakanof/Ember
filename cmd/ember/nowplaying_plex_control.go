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
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

const plexClientID = "ember-nowplaying"

const (
	plexVolumeTTL     = 5 * time.Second
	plexVolumeRefresh = 30 * time.Second
)

type plexControl struct {
	now       func() time.Time
	mu        sync.Mutex
	targets   map[string]string
	volumes   map[string]plexLevel
	commanded map[string]plexCommand
	volMu     sync.Mutex
	cmdID     atomic.Int64
}

type plexLevel struct {
	v  int
	at time.Time
}

type plexCommand struct {
	state nowplaying.State
	at    time.Time
}

func newPlexControl() plexControl {
	return plexControl{now: time.Now, targets: make(map[string]string), volumes: make(map[string]plexLevel),
		commanded: make(map[string]plexCommand)}
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

func (c *plexControl) volume(target string, maxAge time.Duration) *int {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.volumes[target]
	if !ok || c.now().Sub(l.at) > maxAge {
		return nil
	}
	return &l.v
}

func (c *plexControl) setVolume(target string, v int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.volumes) >= 16 {
		clear(c.volumes)
	}
	c.volumes[target] = plexLevel{v, c.now()}
}

func (c *plexControl) playState(target string, shown nowplaying.State) nowplaying.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd, ok := c.commanded[target]; ok && c.now().Sub(cmd.at) < plexCommandedTTL {
		return cmd.state
	}
	return shown
}

func (c *plexControl) setCommanded(target string, st nowplaying.State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.commanded) >= 16 {
		clear(c.commanded)
	}
	c.commanded[target] = plexCommand{st, c.now()}
}

func (p *plexSource) control(ctx context.Context, e nowplaying.Entry, req controlRequest) (controlResult, error) {
	target := p.ctl.target(e.Player)
	if target == "" {
		return controlResult{}, errNoController
	}
	res := controlResult{Status: "done", Source: plexSourceID}
	var err error
	switch req.Action {
	case actPlayPause, actPlay, actPause:
		want := nowplaying.Playing
		switch {
		case req.Action == actPause:
			want = nowplaying.Paused
		case req.Action == actPlayPause && p.ctl.playState(target, e.State) == nowplaying.Playing:
			want = nowplaying.Paused
		}
		cmd := "play"
		if want == nowplaying.Paused {
			cmd = "pause"
		}
		if err = p.command(ctx, target, cmd, nil); err == nil {
			p.ctl.setCommanded(target, want)
		}
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
	p.wake()
	return res, nil
}

func (p *plexSource) stepVolume(ctx context.Context, target string, delta int) (int, error) {
	p.ctl.volMu.Lock()
	defer p.ctl.volMu.Unlock()
	cur := p.ctl.volume(target, plexVolumeTTL)
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
	req.Header.Del("Accept")
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
