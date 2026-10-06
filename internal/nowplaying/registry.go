package nowplaying

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type State string

const (
	Playing State = "playing"
	Paused  State = "paused"
	Stopped State = "stopped"
)

type Kind string

const (
	Album    Kind = "album"
	Artist   Kind = "artist"
	Backdrop Kind = "backdrop"
)

const (
	PausedTTL       = 10 * time.Minute
	PlayingGrace    = 5 * time.Minute
	NoDurationTTL   = 30 * time.Minute
	SeekTolerance   = 3 * time.Second
	ForgetAfter     = time.Hour
	maxEntries      = 32
	maxTextRunes    = 200
	maxPlayerRunes  = 64
	maxTrackIDRunes = 128
)

var (
	ErrInvalid       = errors.New("invalid now-playing report")
	ErrNoEntry       = errors.New("no now-playing entry for this source and player")
	ErrTrackMismatch = errors.New("track_id is not the entry's current track")
)

var sourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type Report struct {
	Source     string `json:"source"`
	Player     string `json:"player"`
	State      State  `json:"state"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	TrackID    string `json:"track_id"`
	DurationMS int64  `json:"duration_ms"`
	PositionMS int64  `json:"position_ms"`
	// 0-100; nil keeps the player's last known volume.
	Volume *int `json:"volume,omitempty"`
}

func (r Report) Validate() error {
	if !sourcePattern.MatchString(r.Source) {
		return fmt.Errorf("%w: source must match %s", ErrInvalid, sourcePattern)
	}
	switch r.State {
	case Playing, Paused, Stopped:
	default:
		return fmt.Errorf("%w: state must be playing, paused or stopped", ErrInvalid)
	}
	for _, f := range []struct {
		name, v string
		max     int
	}{
		{"player", r.Player, maxPlayerRunes},
		{"title", r.Title, maxTextRunes},
		{"artist", r.Artist, maxTextRunes},
		{"album", r.Album, maxTextRunes},
		{"track_id", r.TrackID, maxTrackIDRunes},
	} {
		if !utf8.ValidString(f.v) || utf8.RuneCountInString(f.v) > f.max {
			return fmt.Errorf("%w: %s must be valid UTF-8 of at most %d characters", ErrInvalid, f.name, f.max)
		}
	}
	if r.DurationMS < 0 || r.PositionMS < 0 {
		return fmt.Errorf("%w: duration_ms and position_ms must be >= 0", ErrInvalid)
	}
	if r.Volume != nil && (*r.Volume < 0 || *r.Volume > 100) {
		return fmt.Errorf("%w: volume must be 0-100", ErrInvalid)
	}
	return nil
}

func (r Report) equal(o Report) bool {
	rv, ov := r.Volume, o.Volume
	r.Volume, o.Volume = nil, nil
	if r != o || (rv == nil) != (ov == nil) {
		return false
	}
	return rv == nil || *rv == *ov
}

func (r Report) track() string {
	if r.TrackID != "" {
		return "id:" + r.TrackID
	}
	return "tag:" + r.Title + "\x00" + r.Artist + "\x00" + r.Album
}

type Image struct {
	Data []byte
	Hash string
}

func NewImage(data []byte) *Image {
	sum := sha256.Sum256(data)
	return &Image{Data: data, Hash: hex.EncodeToString(sum[:8])}
}

type Entry struct {
	Report
	PositionAt time.Time
	StateSince time.Time
	UpdatedAt  time.Time
	AlbumArt   *Image
	ArtistArt  *Image
}

func (e Entry) Position(now time.Time) int64 {
	p := e.PositionMS
	if e.State == Playing && now.After(e.PositionAt) {
		p += now.Sub(e.PositionAt).Milliseconds()
	}
	if e.DurationMS > 0 && p > e.DurationMS {
		p = e.DurationMS
	}
	return p
}

func (e Entry) ArtVersion() string {
	if e.AlbumArt == nil && e.ArtistArt == nil {
		return ""
	}
	var a, b string
	if e.AlbumArt != nil {
		a = e.AlbumArt.Hash
	}
	if e.ArtistArt != nil {
		b = e.ArtistArt.Hash
	}
	sum := sha256.Sum256([]byte(a + "|" + b))
	return hex.EncodeToString(sum[:6])
}

func (e Entry) Picture(k Kind) *Image {
	switch k {
	case Album:
		return e.AlbumArt
	case Artist:
		return e.ArtistArt
	case Backdrop:
		if e.ArtistArt != nil {
			return e.ArtistArt
		}
		return e.AlbumArt
	}
	return nil
}

func (e Entry) expired(now time.Time) bool {
	switch e.State {
	case Paused:
		return now.Sub(e.StateSince) >= PausedTTL
	case Playing:
		if e.DurationMS > 0 {
			end := e.PositionAt.Add(time.Duration(e.DurationMS-e.PositionMS) * time.Millisecond)
			return now.After(end.Add(PlayingGrace))
		}
		return now.Sub(e.UpdatedAt) >= NoDurationTTL
	}
	return true
}

type key struct{ source, player string }

type Registry struct {
	mu      sync.Mutex
	entries map[key]*Entry
	// Runs under mu: it must not block or call back into the registry.
	OnChange func()
}

func (r *Registry) changedLocked() {
	if r.OnChange != nil {
		r.OnChange()
	}
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[key]*Entry)}
}

func (r *Registry) Report(rep Report, now time.Time) (bool, error) {
	if err := rep.Validate(); err != nil {
		return false, err
	}
	k := key{rep.Source, rep.Player}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rep.State == Stopped {
		_, had := r.entries[k]
		delete(r.entries, k)
		if had {
			r.changedLocked()
		}
		return had, nil
	}
	old := r.entries[k]
	if old == nil {
		r.evictLocked(now)
	}
	if rep.Volume == nil && old != nil {
		rep.Volume = old.Volume
	}
	next := &Entry{Report: rep, PositionAt: now, StateSince: now, UpdatedAt: now}
	if old == nil || old.track() != rep.track() {
		r.entries[k] = next
		r.changedLocked()
		return true, nil
	}
	next.AlbumArt, next.ArtistArt = old.AlbumArt, old.ArtistArt
	if old.State == rep.State {
		next.StateSince = old.StateSince
		drift := time.Duration(rep.PositionMS-old.Position(now)) * time.Millisecond
		if drift.Abs() <= SeekTolerance && old.DurationMS == rep.DurationMS {
			next.PositionMS, next.PositionAt = old.PositionMS, old.PositionAt
		}
	}
	r.entries[k] = next
	same := rep
	same.PositionMS = old.PositionMS
	if !same.equal(old.Report) || !next.PositionAt.Equal(old.PositionAt) {
		r.changedLocked()
	}
	return false, nil
}

func (r *Registry) SetArt(source, player, trackID string, kind Kind, img *Image) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[key{source, player}]
	if e == nil {
		return ErrNoEntry
	}
	if trackID != "" && trackID != e.TrackID {
		return ErrTrackMismatch
	}
	c := *e
	switch kind {
	case Album:
		c.AlbumArt = img
	case Artist:
		c.ArtistArt = img
	default:
		return fmt.Errorf("%w: art kind must be album or artist", ErrInvalid)
	}
	r.entries[key{source, player}] = &c
	r.changedLocked()
	return nil
}

func (r *Registry) SetArtistArt(source, player, artist string, img *Image) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[key{source, player}]
	if e == nil || e.Artist != artist || e.ArtistArt != nil {
		return false
	}
	c := *e
	c.ArtistArt = img
	r.entries[key{source, player}] = &c
	r.changedLocked()
	return true
}

func (r *Registry) Get(source, player string) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[key{source, player}]
	if e == nil {
		return Entry{}, false
	}
	return *e, true
}

func (r *Registry) Remove(source, player string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, had := r.entries[key{source, player}]; had {
		delete(r.entries, key{source, player})
		r.changedLocked()
	}
}

func (r *Registry) evictLocked(now time.Time) {
	var oldest key
	var oldestAt time.Time
	for k, e := range r.entries {
		if now.Sub(e.UpdatedAt) >= ForgetAfter {
			delete(r.entries, k)
			continue
		}
		if oldestAt.IsZero() || e.UpdatedAt.Before(oldestAt) {
			oldest, oldestAt = k, e.UpdatedAt
		}
	}
	if len(r.entries) >= maxEntries {
		delete(r.entries, oldest)
	}
}

func (r *Registry) Current(now time.Time) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var live []*Entry
	for _, e := range r.entries {
		if !e.expired(now) {
			live = append(live, e)
		}
	}
	if len(live) == 0 {
		return Entry{}, false
	}
	sort.Slice(live, func(i, j int) bool {
		a, b := live[i], live[j]
		if (a.State == Playing) != (b.State == Playing) {
			return a.State == Playing
		}
		if !a.StateSince.Equal(b.StateSince) {
			return a.StateSince.After(b.StateSince)
		}
		return strings.Compare(a.Source+"\x00"+a.Player, b.Source+"\x00"+b.Player) < 0
	})
	return *live[0], true
}
