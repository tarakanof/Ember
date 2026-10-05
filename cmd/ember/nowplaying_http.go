package main

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

// nowPlayingState is GET /v1/nowplaying/state, readable by anyone on the
// LAN, so it leaves out the player (a Mac's name). state is playing, paused
// or none; every other field is null with none. position_ms is the position
// at position_at (Unix ms): clients extrapolate while playing.
type nowPlayingState struct {
	State        string  `json:"state"`
	Source       *string `json:"source"`
	Title        *string `json:"title"`
	Artist       *string `json:"artist"`
	Album        *string `json:"album"`
	DurationMS   *int64  `json:"duration_ms"`
	PositionMS   *int64  `json:"position_ms"`
	PositionAt   *int64  `json:"position_at"`
	UpdatedAt    *string `json:"updated_at"`
	ArtVersion   *string `json:"art_version"`
	HasAlbumArt  bool    `json:"has_album_art"`
	HasArtistArt bool    `json:"has_artist_art"`
}

const nowPlayingNone = "none"

func (a *App) nowPlayingState(now time.Time) nowPlayingState {
	e, ok := a.nowPlaying.reg.Current(now)
	if !ok {
		return nowPlayingState{State: nowPlayingNone}
	}
	str := func(s string) *string { return &s }
	num := func(n int64) *int64 { return &n }
	s := nowPlayingState{
		State:        string(e.State),
		Source:       str(e.Source),
		Title:        str(e.Title),
		Artist:       str(e.Artist),
		Album:        str(e.Album),
		PositionMS:   num(e.PositionMS),
		PositionAt:   num(e.PositionAt.UnixMilli()),
		UpdatedAt:    str(e.UpdatedAt.UTC().Format(time.RFC3339)),
		HasAlbumArt:  e.AlbumArt != nil,
		HasArtistArt: e.ArtistArt != nil,
	}
	if e.DurationMS > 0 {
		s.DurationMS = num(e.DurationMS)
	}
	if v := e.ArtVersion(); v != "" {
		s.ArtVersion = str(v)
	}
	return s
}

func (a *App) handleNowPlayingState(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	w.Header().Set("Cache-Control", "no-cache")
	// The server's Unix seconds, so a client with a skewed clock extrapolates
	// position_at the way the knob does.
	w.Header().Set(knobNowHeader, strconv.FormatInt(now.Unix(), 10))
	writeJSON(w, http.StatusOK, a.nowPlayingState(now))
}

// handleNowPlayingArt serves the current entry's picture as a square
// baseline JPEG. ?v= naming the current art_version makes it cacheable.
func (a *App) handleNowPlayingArt(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := nowplaying.Kind(q.Get("kind"))
	sizes, ok := nowplaying.Sizes[kind]
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("kind must be album, artist or backdrop"))
		return
	}
	size := sizes[0]
	if s := q.Get("size"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || !slices.Contains(sizes, n) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("size for %s must be one of %v", kind, sizes))
			return
		}
		size = n
	}
	e, ok := a.nowPlaying.reg.Current(time.Now())
	var img *nowplaying.Image
	if ok {
		img = e.Picture(kind)
	}
	if img == nil {
		writeError(w, http.StatusNotFound, errors.New("no artwork"))
		return
	}
	etag := artETag(img, kind, size)
	h := w.Header()
	h.Set("ETag", etag)
	if v := q.Get("v"); v != "" && v == e.ArtVersion() {
		h.Set("Cache-Control", "public, max-age=86400, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body, err := a.nowPlaying.render(img, kind, size)
	if err != nil {
		a.logger.Warn("artwork render failed", "kind", kind, "size", size, "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("artwork render failed"))
		return
	}
	h.Set("Content-Type", "image/jpeg")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// nowPlayingAck answers an ingest: which pictures the server holds for the
// player, so a pusher sends artwork only when it is missing.
type nowPlayingAck struct {
	HasAlbumArt  bool `json:"has_album_art"`
	HasArtistArt bool `json:"has_artist_art"`
}

// handleNowPlayingReport is POST /v1/nowplaying. Unknown fields are
// ignored, like /v1/status, so newer pushers work with older servers.
func (a *App) handleNowPlayingReport(w http.ResponseWriter, r *http.Request) {
	var rep nowplaying.Report
	if !a.decodeOrReject(w, r, &rep, false) {
		return
	}
	if err := a.nowPlaying.report(rep, time.Now()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	e, _ := a.nowPlaying.reg.Get(rep.Source, rep.Player)
	writeJSON(w, http.StatusOK, nowPlayingAck{HasAlbumArt: e.AlbumArt != nil, HasArtistArt: e.ArtistArt != nil})
}

// handleNowPlayingArtPut is PUT /v1/nowplaying/art: a raw JPEG or PNG for
// one player's current track (409 when track_id is no longer current).
func (a *App) handleNowPlayingArtPut(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := nowplaying.Kind(q.Get("kind"))
	if kind != nowplaying.Album && kind != nowplaying.Artist {
		writeError(w, http.StatusBadRequest, errors.New("kind must be album or artist"))
		return
	}
	if q.Get("track_id") == "" {
		writeError(w, http.StatusBadRequest, errors.New("track_id is required"))
		return
	}
	img, err := readArt(http.MaxBytesReader(w, r.Body, nowplaying.MaxArtBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			a.rejectBody(w, r, err)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	switch err := a.nowPlaying.reg.SetArt(q.Get("source"), q.Get("player"), q.Get("track_id"), kind, img); {
	case errors.Is(err, nowplaying.ErrNoEntry):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, nowplaying.ErrTrackMismatch):
		writeError(w, http.StatusConflict, err)
	case err != nil:
		writeError(w, http.StatusBadRequest, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
