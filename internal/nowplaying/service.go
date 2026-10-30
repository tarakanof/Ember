package nowplaying

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	nowPlayingCacheEntries = 32
	nowPlayingCacheBytes   = 8 << 20
)

type Service struct {
	Reg          *Registry
	Cache        *Cache
	renderMu     sync.Mutex
	Artists      *ArtistLookup
	Jobs         chan ArtistJob
	Plex         *PlexSource
	Commands     *CommandQueue
	ControlKeys  ControlKeys
	ControlLimit *CallerLimiter
}

type ArtistJob struct{ source, player, artist string }

func NewService() *Service {
	return &Service{
		Reg:          NewRegistry(),
		Cache:        NewCache(nowPlayingCacheEntries, nowPlayingCacheBytes),
		Jobs:         make(chan ArtistJob, 4),
		Commands:     newCommandQueue(),
		ControlLimit: &CallerLimiter{Burst: ControlBurst, PerSec: ControlPerSec},
	}
}

func (s *Service) Report(rep Report, now time.Time) error {
	if _, err := s.Reg.Report(rep, now); err != nil {
		return err
	}
	s.wantArtist(rep.Source, rep.Player)
	return nil
}

func (s *Service) wantArtist(source, player string) {
	if s.Artists == nil {
		return
	}
	e, ok := s.Reg.Get(source, player)
	if !ok || e.Artist == "" || e.ArtistArt != nil {
		return
	}
	select {
	case s.Jobs <- ArtistJob{source, player, e.Artist}:
	default:
	}
}

func (s *Service) Render(img *Image, kind Kind, size int) ([]byte, error) {
	k := ArtETag(img, kind, size)
	if b, ok := s.Cache.Get(k); ok {
		return b, nil
	}
	s.renderMu.Lock()
	defer s.renderMu.Unlock()
	if b, ok := s.Cache.Get(k); ok {
		return b, nil
	}
	b, err := Render(img.Data, kind, size)
	if err != nil {
		return nil, err
	}
	s.Cache.Put(k, b)
	return b, nil
}

func ArtETag(img *Image, kind Kind, size int) string {
	if kind == Cover {
		return fmt.Sprintf(`"%s-%s-%d-%s"`, img.Hash, kind, size, CoverRecipe)
	}
	return fmt.Sprintf(`"%s-%s-%d"`, img.Hash, kind, size)
}

func (s *Service) LookupArtist(ctx context.Context, j ArtistJob, logger *slog.Logger) {
	img, err := s.Artists.Find(ctx, j.artist)
	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("artist picture lookup failed", "err", err)
		}
		return
	}
	if img != nil {
		s.Reg.SetArtistArt(j.source, j.player, j.artist, img)
	}
}

func ReadArt(r io.Reader) (*Image, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxArtBytes+1))
	if err != nil {
		return nil, err
	}
	return Prepare(b)
}

func fetchArt(ctx context.Context, client *http.Client, url string, header http.Header) (*Image, error) {
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
	return ReadArt(resp.Body)
}
