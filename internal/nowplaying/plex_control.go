package nowplaying

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
)

const plexClientID = "ember-nowplaying"

const (
	PlexVolumeTTL     = 5 * time.Second
	plexVolumeRefresh = 30 * time.Second
)

type PlexControl struct {
	Now       func() time.Time
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
	state State
	at    time.Time
}

func newPlexControl() PlexControl {
	return PlexControl{Now: time.Now, targets: make(map[string]string), volumes: make(map[string]plexLevel),
		commanded: make(map[string]plexCommand)}
}

func (c *PlexControl) setTarget(player, target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.targets) >= 16 {
		clear(c.targets)
	}
	c.targets[player] = target
}

func (c *PlexControl) target(player string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.targets[player]
}

func (c *PlexControl) volume(target string, maxAge time.Duration) *int {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.volumes[target]
	if !ok || c.Now().Sub(l.at) > maxAge {
		return nil
	}
	return &l.v
}

func (c *PlexControl) setVolume(target string, v int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.volumes) >= 16 {
		clear(c.volumes)
	}
	c.volumes[target] = plexLevel{v, c.Now()}
}

func (c *PlexControl) playState(target string, shown State) State {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd, ok := c.commanded[target]; ok && c.Now().Sub(cmd.at) < plexCommandedTTL {
		return cmd.state
	}
	return shown
}

func (c *PlexControl) setCommanded(target string, st State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.commanded) >= 16 {
		clear(c.commanded)
	}
	c.commanded[target] = plexCommand{st, c.Now()}
}

func (p *PlexSource) control(ctx context.Context, e Entry, req ControlRequest) (ControlResult, error) {
	target := p.Ctl.target(e.Player)
	if target == "" {
		return ControlResult{}, ErrNoController
	}
	res := ControlResult{Status: "done", Source: plexSourceID}
	var err error
	switch req.Action {
	case actPlayPause, actPlay, actPause:
		want := Playing
		switch {
		case req.Action == actPause:
			want = Paused
		case req.Action == actPlayPause && p.Ctl.playState(target, e.State) == Playing:
			want = Paused
		}
		cmd := "play"
		if want == Paused {
			cmd = "pause"
		}
		if err = p.command(ctx, target, cmd, nil); err == nil {
			p.Ctl.setCommanded(target, want)
		}
	case ActNext:
		err = p.command(ctx, target, "skipNext", nil)
	case actPrevious:
		err = p.command(ctx, target, "skipPrevious", nil)
	case ActVolume:
		var v int
		v, err = p.stepVolume(ctx, target, req.Delta)
		res.Volume = &v
	}
	if err != nil {
		return ControlResult{}, fmt.Errorf("%w: %w", errControlFailed, err)
	}
	p.Wake()
	return res, nil
}

func (p *PlexSource) stepVolume(ctx context.Context, target string, delta int) (int, error) {
	p.Ctl.volMu.Lock()
	defer p.Ctl.volMu.Unlock()
	cur := p.Ctl.volume(target, PlexVolumeTTL)
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
	p.Ctl.setVolume(target, v)
	return v, nil
}

func (p *PlexSource) playerRequest(ctx context.Context, target, path string, q url.Values) (*http.Response, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("type", "music")
	q.Set("commandID", strconv.FormatInt(p.Ctl.cmdID.Add(1), 10))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Cfg.URL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header = p.headers()
	req.Header.Del("Accept")
	req.Header.Set("X-Plex-Target-Client-Identifier", target)
	req.Header.Set("X-Plex-Client-Identifier", plexClientID)
	return p.client.Do(req)
}

func (p *PlexSource) command(ctx context.Context, target, cmd string, q url.Values) error {
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

func (p *PlexSource) timelineVolume(ctx context.Context, target string) (int, error) {
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
		p.Ctl.setVolume(target, v)
		return v, nil
	}
	return 0, fmt.Errorf("plex timeline: no music volume")
}
