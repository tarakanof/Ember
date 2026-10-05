package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

// Deezer artist search: no key; non-commercial use, and its images must not
// be stored, so hits live only in this RAM map (and the render cache).
const (
	deezerAPIBase       = "https://api.deezer.com"
	artistLookupMax     = 64
	artistLookupBytes   = 4 << 20
	artistMissTTL       = time.Hour
	artistErrTTL        = time.Minute
	artistLookupTimeout = 10 * time.Second
)

// artistLookup finds an artist picture by name and remembers the answer.
type artistLookup struct {
	base   string
	client *http.Client
	now    func() time.Time
	// pictureOK vets a picture URL from a search answer before it is
	// fetched (and later served publicly): https on Deezer's CDN only.
	pictureOK func(*url.URL) bool

	mu   sync.Mutex // protects hits
	hits map[string]artistHit
}

type artistHit struct {
	img *nowplaying.Image // nil = no picture
	err error             // a failed lookup, retried after artistErrTTL
	at  time.Time
}

func newArtistLookup(base string) *artistLookup {
	return &artistLookup{
		base: strings.TrimRight(base, "/"),
		client: &http.Client{
			Timeout: artistLookupTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now:       time.Now,
		hits:      make(map[string]artistHit),
		pictureOK: deezerCDN,
	}
}

func deezerCDN(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return u.Scheme == "https" && (h == "dzcdn.net" || strings.HasSuffix(h, ".dzcdn.net"))
}

// find returns the artist's picture, nil when Deezer has none.
func (l *artistLookup) find(ctx context.Context, artist string) (*nowplaying.Image, error) {
	name := strings.ToLower(strings.TrimSpace(artist))
	if name == "" {
		return nil, nil
	}
	l.mu.Lock()
	if h, ok := l.hits[name]; ok {
		age := l.now().Sub(h.at)
		switch {
		case h.err != nil && age < artistErrTTL:
			l.mu.Unlock()
			return nil, nil
		case h.err == nil && (h.img != nil || age < artistMissTTL):
			l.mu.Unlock()
			return h.img, nil
		}
	}
	l.mu.Unlock()

	img, err := l.search(ctx, name)
	if err != nil && ctx.Err() != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, name)
	for len(l.hits) >= artistLookupMax || l.bytesLocked()+imgLen(img) > artistLookupBytes {
		if len(l.hits) == 0 {
			break
		}
		var oldest string
		for k, h := range l.hits {
			if oldest == "" || h.at.Before(l.hits[oldest].at) {
				oldest = k
			}
		}
		delete(l.hits, oldest)
	}
	l.hits[name] = artistHit{img: img, err: err, at: l.now()}
	return img, err
}

func (l *artistLookup) bytesLocked() int {
	n := 0
	for _, h := range l.hits {
		n += imgLen(h.img)
	}
	return n
}

func imgLen(img *nowplaying.Image) int {
	if img == nil {
		return 0
	}
	return len(img.Data)
}

func (l *artistLookup) search(ctx context.Context, name string) (*nowplaying.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, artistLookupTimeout)
	defer cancel()
	u := l.base + "/search/artist?limit=5&q=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deezer search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deezer search: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Name       string `json:"name"`
			PictureXL  string `json:"picture_xl"`
			PictureBig string `json:"picture_big"`
		} `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("deezer search: %w", err)
	}
	pick := -1
	for i, d := range body.Data {
		if strings.EqualFold(strings.TrimSpace(d.Name), name) {
			pick = i
			break
		}
	}
	if pick < 0 {
		return nil, nil
	}
	pic := body.Data[pick].PictureXL
	if pic == "" {
		pic = body.Data[pick].PictureBig
	}
	if pic == "" || strings.Contains(pic, "/artist//") {
		return nil, nil
	}
	pu, err := url.Parse(strings.Replace(pic, "/1000x1000-", "/500x500-", 1))
	if err != nil || !l.pictureOK(pu) {
		return nil, nil
	}
	img, err := fetchArt(ctx, l.client, pu.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("deezer picture: %w", err)
	}
	return img, nil
}
