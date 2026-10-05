package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

// Rendered-art cache bounds: three kinds per track for a few tracks, plus
// odd sizes, well under the container's memory budget.
const (
	nowPlayingCacheEntries = 32
	nowPlayingCacheBytes   = 8 << 20
)

// nowPlayingService owns the now-playing registry, the rendered-art cache
// and the background artist lookups.
type nowPlayingService struct {
	reg   *nowplaying.Registry
	cache *nowplaying.Cache
	// renderMu serialises art renders so concurrent cache misses can't
	// multiply the decode buffers (a 1400 px source is ~20 MB transient).
	renderMu sync.Mutex
	// artists finds artist pictures by name; nil turns lookups off.
	artists *artistLookup
	// jobs queues artist lookups; 4 absorbs a burst of track changes from
	// several sources, and a full queue drops the job (the next report
	// for that player queues it again).
	jobs chan artistJob
	plex *plexSource
}

type artistJob struct{ source, player, artist string }

func newNowPlayingService() *nowPlayingService {
	return &nowPlayingService{
		reg:   nowplaying.NewRegistry(),
		cache: nowplaying.NewCache(nowPlayingCacheEntries, nowPlayingCacheBytes),
		jobs:  make(chan artistJob, 4),
	}
}

// report records a source's report and queues an artist lookup when the
// player has an artist but no picture.
func (s *nowPlayingService) report(rep nowplaying.Report, now time.Time) error {
	if _, err := s.reg.Report(rep, now); err != nil {
		return err
	}
	s.wantArtist(rep.Source, rep.Player)
	return nil
}

func (s *nowPlayingService) wantArtist(source, player string) {
	if s.artists == nil {
		return
	}
	e, ok := s.reg.Get(source, player)
	if !ok || e.Artist == "" || e.ArtistArt != nil {
		return
	}
	select {
	case s.jobs <- artistJob{source, player, e.Artist}:
	default:
	}
}

// render returns the JPEG for the current entry's picture of kind at size,
// from the cache when it can.
func (s *nowPlayingService) render(img *nowplaying.Image, kind nowplaying.Kind, size int) ([]byte, error) {
	k := artETag(img, kind, size)
	if b, ok := s.cache.Get(k); ok {
		return b, nil
	}
	s.renderMu.Lock()
	defer s.renderMu.Unlock()
	if b, ok := s.cache.Get(k); ok {
		return b, nil
	}
	b, err := nowplaying.Render(img.Data, kind, size)
	if err != nil {
		return nil, err
	}
	s.cache.Put(k, b)
	return b, nil
}

func artETag(img *nowplaying.Image, kind nowplaying.Kind, size int) string {
	return fmt.Sprintf(`"%s-%s-%d"`, img.Hash, kind, size)
}

// StartNowPlaying runs the artist lookups and, when configured, the Plex
// poller until ctx ends.
func (a *App) StartNowPlaying(ctx context.Context) {
	np := a.nowPlaying
	var plex sync.WaitGroup
	defer plex.Wait()
	if np.plex != nil {
		plex.Go(func() { np.plex.run(ctx, np, a.logger) })
	}
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-np.jobs:
			np.lookupArtist(ctx, j, a)
		}
	}
}

func (s *nowPlayingService) lookupArtist(ctx context.Context, j artistJob, a *App) {
	img, err := s.artists.find(ctx, j.artist)
	if err != nil {
		if ctx.Err() == nil {
			a.logger.Warn("artist picture lookup failed", "err", err)
		}
		return
	}
	if img != nil {
		s.reg.SetArtistArt(j.source, j.player, j.artist, img)
	}
}

// readArt reads at most nowplaying.MaxArtBytes from r and checks the result
// is a JPEG or PNG within the limits.
func readArt(r io.Reader) (*nowplaying.Image, error) {
	b, err := io.ReadAll(io.LimitReader(r, nowplaying.MaxArtBytes+1))
	if err != nil {
		return nil, err
	}
	if err := nowplaying.CheckImage(b); err != nil {
		return nil, err
	}
	return nowplaying.NewImage(b), nil
}

// fetchArt GETs an image URL with optional headers.
func fetchArt(ctx context.Context, client *http.Client, url string, header http.Header) (*nowplaying.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork: HTTP %d", resp.StatusCode)
	}
	return readArt(resp.Body)
}
