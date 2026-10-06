package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/nowplaying"
)

const (
	plexPollPlaying = 2 * time.Second
	plexPollIdle    = 10 * time.Second
	plexTimeout     = 5 * time.Second
	plexArtPx       = 480
	plexSourceID    = "plex"
)

type plexConfig struct {
	URL, Token, User, Player, WebhookKey string
}

func (c plexConfig) LogValue() slog.Value {
	return slog.GroupValue(slog.String("url", c.URL), slog.Bool("token_set", c.Token != ""),
		slog.String("user", c.User), slog.String("player", c.Player), slog.Bool("webhook", c.WebhookKey != ""))
}

func plexConfigFromEnv(getenv func(string) string) (plexConfig, bool) {
	c := plexConfig{
		URL:        strings.TrimRight(strings.TrimSpace(getenv("EMBER_PLEX_URL")), "/"),
		Token:      strings.TrimSpace(getenv("EMBER_PLEX_TOKEN")),
		User:       strings.TrimSpace(getenv("EMBER_PLEX_USER")),
		Player:     strings.TrimSpace(getenv("EMBER_PLEX_PLAYER")),
		WebhookKey: strings.TrimSpace(getenv("EMBER_PLEX_WEBHOOK_KEY")),
	}
	u, err := url.Parse(c.URL)
	if c.Token == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return c, false
	}
	return c, true
}

type plexSource struct {
	cfg    plexConfig
	client *http.Client
	nudge  chan struct{}

	player     string
	albumPath  string
	artistPath string
	album      *nowplaying.Image
	artist     *nowplaying.Image
	lastErr    string
	artFailed  map[string]bool
	track      string
	offset     int64

	volTried  time.Time
	volTarget string

	ctl plexControl
}

func newPlexSource(cfg plexConfig) *plexSource {
	client := &http.Client{
		Timeout: plexTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &plexSource{cfg: cfg, client: client, nudge: make(chan struct{}, 1), artFailed: make(map[string]bool),
		ctl: newPlexControl()}
}

func (p *plexSource) wake() {
	select {
	case p.nudge <- struct{}{}:
	default:
	}
}

func (p *plexSource) run(ctx context.Context, np *nowPlayingService, logger *slog.Logger) {
	for {
		wait := plexPollIdle
		if p.poll(ctx, np, logger, time.Now()) {
			wait = plexPollPlaying
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-p.nudge:
			t.Stop()
		case <-t.C:
		}
	}
}

type plexSession struct {
	Type             string `json:"type"`
	RatingKey        string `json:"ratingKey"`
	Title            string `json:"title"`
	ParentTitle      string `json:"parentTitle"`
	GrandparentTitle string `json:"grandparentTitle"`
	OriginalTitle    string `json:"originalTitle"`
	Thumb            string `json:"thumb"`
	ParentThumb      string `json:"parentThumb"`
	GrandparentThumb string `json:"grandparentThumb"`
	Duration         int64  `json:"duration"`
	ViewOffset       int64  `json:"viewOffset"`
	User             struct {
		Title string `json:"title"`
	} `json:"User"`
	Player struct {
		Title             string `json:"title"`
		State             string `json:"state"`
		MachineIdentifier string `json:"machineIdentifier"`
	} `json:"Player"`
}

func (p *plexSource) poll(ctx context.Context, np *nowPlayingService, logger *slog.Logger, now time.Time) bool {
	s, err := p.session(ctx)
	if err != nil {
		if ctx.Err() == nil && err.Error() != p.lastErr {
			logger.Warn("plex poll failed", "err", err)
		}
		p.lastErr = err.Error()
		return false
	}
	if p.lastErr != "" {
		logger.Info("plex poll recovered")
		p.lastErr = ""
	}
	if s == nil {
		if p.player != "" {
			np.reg.Remove(plexSourceID, p.player)
			p.player = ""
		}
		return false
	}
	player := truncRunes(s.Player.Title, 64)
	if player == "" {
		player = truncRunes(s.Player.MachineIdentifier, 64)
	}
	if p.player != "" && p.player != player {
		np.reg.Remove(plexSourceID, p.player)
	}
	p.player = player
	target := s.Player.MachineIdentifier
	p.ctl.setTarget(player, target)
	artist := s.GrandparentTitle
	if s.OriginalTitle != "" {
		artist = s.OriginalTitle
	}
	state := nowplaying.Paused
	if s.Player.State == "playing" || s.Player.State == "buffering" {
		state = nowplaying.Playing
	}
	rep := nowplaying.Report{
		Source: plexSourceID, Player: player, State: state,
		Title: truncRunes(s.Title, 200), Artist: truncRunes(artist, 200), Album: truncRunes(s.ParentTitle, 200),
		TrackID: truncRunes(s.RatingKey, 128), DurationMS: max(s.Duration, 0), PositionMS: max(s.ViewOffset, 0),
		Volume: p.volumeFor(ctx, target, s.Player.State == "playing"),
	}
	if rep.TrackID == p.track && s.ViewOffset == p.offset {
		if e, ok := np.reg.Get(plexSourceID, player); ok && e.TrackID == rep.TrackID && e.State == state {
			rep.PositionMS = e.Position(now)
		}
	}
	p.track, p.offset = rep.TrackID, s.ViewOffset
	if _, err := np.reg.Report(rep, now); err != nil {
		logger.Warn("plex session rejected", "err", err)
		return false
	}
	albumPath := s.ParentThumb
	if albumPath == "" {
		albumPath = s.Thumb
	}
	p.album = p.art(ctx, albumPath, &p.albumPath, p.album, logger)
	p.artist = p.art(ctx, s.GrandparentThumb, &p.artistPath, p.artist, logger)
	if p.album != nil {
		_ = np.reg.SetArt(plexSourceID, player, rep.TrackID, nowplaying.Album, p.album)
	}
	if p.artist != nil {
		_ = np.reg.SetArt(plexSourceID, player, rep.TrackID, nowplaying.Artist, p.artist)
	}
	np.wantArtist(plexSourceID, player)
	return state == nowplaying.Playing
}

func (p *plexSource) volumeFor(ctx context.Context, target string, playing bool) *int {
	if target == "" {
		return nil
	}
	if v := p.ctl.volume(target, plexVolumeRefresh); v != nil || !playing {
		return v
	}
	if now := p.ctl.now(); now.Sub(p.volTried) >= plexVolumeRefresh || p.volTarget != target {
		p.volTried, p.volTarget = now, target
	} else {
		return nil
	}
	if v, err := p.timelineVolume(ctx, target); err == nil {
		return &v
	}
	return nil
}

func (p *plexSource) art(ctx context.Context, path string, last *string, prev *nowplaying.Image, logger *slog.Logger) *nowplaying.Image {
	if path == *last {
		return prev
	}
	if path == "" {
		*last = ""
		return nil
	}
	u := fmt.Sprintf("%s/photo/:/transcode?width=%d&height=%d&minSize=1&upscale=1&url=%s",
		p.cfg.URL, plexArtPx, plexArtPx, url.QueryEscape(path))
	img, err := fetchArt(ctx, p.client, u, p.headers())
	if err != nil {
		if !p.artFailed[path] {
			logger.Warn("plex artwork fetch failed", "err", err)
		}
		if len(p.artFailed) >= 8 {
			clear(p.artFailed)
		}
		p.artFailed[path] = true
		return nil
	}
	*last = path
	return img
}

func (p *plexSource) headers() http.Header {
	return http.Header{"X-Plex-Token": {p.cfg.Token}, "Accept": {"application/json"}}
}

func (p *plexSource) session(ctx context.Context) (*plexSession, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.URL+"/status/sessions", nil)
	if err != nil {
		return nil, err
	}
	req.Header = p.headers()
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plex sessions: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plex sessions: HTTP %d", resp.StatusCode)
	}
	var body struct {
		MediaContainer struct {
			Metadata []plexSession `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("plex sessions: %w", err)
	}
	var paused *plexSession
	for i := range body.MediaContainer.Metadata {
		s := &body.MediaContainer.Metadata[i]
		if s.Type != "track" || !p.matches(s) {
			continue
		}
		if s.Player.State == "playing" || s.Player.State == "buffering" {
			return s, nil
		}
		if paused == nil {
			paused = s
		}
	}
	return paused, nil
}

func (p *plexSource) matches(s *plexSession) bool {
	if p.cfg.User != "" && !strings.EqualFold(p.cfg.User, s.User.Title) {
		return false
	}
	if p.cfg.Player != "" && !strings.EqualFold(p.cfg.Player, s.Player.Title) && p.cfg.Player != s.Player.MachineIdentifier {
		return false
	}
	return true
}

func truncRunes(s string, n int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func (a *App) handlePlexWebhook(w http.ResponseWriter, r *http.Request) {
	plex := a.nowPlaying.plex
	if plex == nil || plex.cfg.WebhookKey == "" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(plex.cfg.WebhookKey)) != 1 {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	plex.wake()
	w.WriteHeader(http.StatusNoContent)
}
