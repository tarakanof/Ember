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

const (
	nowPlayingCacheEntries = 32
	nowPlayingCacheBytes   = 8 << 20
)

type nowPlayingService struct {
	reg          *nowplaying.Registry
	cache        *nowplaying.Cache
	renderMu     sync.Mutex
	artists      *artistLookup
	jobs         chan artistJob
	plex         *plexSource
	commands     *commandQueue
	controlKeys  controlKeys
	controlLimit *callerLimiter
}

type artistJob struct{ source, player, artist string }

func newNowPlayingService() *nowPlayingService {
	return &nowPlayingService{
		reg:          nowplaying.NewRegistry(),
		cache:        nowplaying.NewCache(nowPlayingCacheEntries, nowPlayingCacheBytes),
		jobs:         make(chan artistJob, 4),
		commands:     newCommandQueue(),
		controlLimit: &callerLimiter{burst: controlBurst, perSec: controlPerSec},
	}
}

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

func readArt(r io.Reader) (*nowplaying.Image, error) {
	b, err := io.ReadAll(io.LimitReader(r, nowplaying.MaxArtBytes+1))
	if err != nil {
		return nil, err
	}
	if err := nowplaying.Validate(b); err != nil {
		return nil, err
	}
	return nowplaying.NewImage(b), nil
}

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
