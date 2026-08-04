package main

import (
	"bytes"
	"context"
	"fmt"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// ensureNativeIcons makes sure every native icon ID that the weather or
// Pomodoro features can reference exists in the clock's /ICONS folder,
// downloading missing ones from the LaMetric gallery and uploading them to
// the device. The device's own on-demand downloads are unreliable (observed
// failing for hours), so the server provisions instead.
//
// A no-op unless a feature that needs native icons is enabled. List failures
// abort the run (no blind re-uploads); per-icon failures are logged and
// skipped — the next config apply or restart retries. Safe to call from any
// goroutine; runs are serialised by iconMu.
func (a *App) ensureNativeIcons(ctx context.Context) {
	cfg := a.cfg.Load()

	need := map[string]bool{}
	for id := range weatherIconIDs(cfg.Weather) {
		need[id] = true
	}
	for id := range pomodoroIconIDs(cfg.Pomodoro) {
		need[id] = true
	}
	if len(need) == 0 {
		return
	}

	a.iconMu.Lock()
	defer a.iconMu.Unlock()

	have, err := a.publisher.ListIcons(ctx)
	if err != nil {
		a.logger.Warn("icon provision: device list failed", "err", err)
		return
	}
	present := map[string]bool{}
	for _, name := range have {
		base := name
		if i := strings.LastIndexByte(name, '.'); i > 0 {
			base = name[:i]
		}
		present[base] = true
	}

	for id := range need {
		if present[id] {
			continue
		}
		data, ext, err := a.iconFetch(ctx, id)
		if err != nil {
			a.logger.Warn("icon provision: gallery fetch failed", "id", id, "err", err)
			continue
		}
		name := id + "." + ext
		if err := a.publisher.PutIcon(ctx, name, data); err != nil {
			a.logger.Warn("icon provision: device upload failed", "name", name, "err", err)
			continue
		}
		a.logger.Info("icon provisioned to device", "name", name)
	}
}

// weatherIconIDs returns the native icon IDs the weather config can reference
// (the six condition buckets, overrides included), or nil if weather doesn't
// need native icons.
func weatherIconIDs(cfg WeatherConfig) map[string]bool {
	if !cfg.Enabled || (!cfg.UseNativeIcons && !cfg.TileNativeIcons) {
		return nil
	}
	ids := map[string]bool{}
	for _, cond := range []string{
		render.WeatherClear, render.WeatherClouds, render.WeatherFog,
		render.WeatherRain, render.WeatherSnow, render.WeatherStorm,
	} {
		if id := cfg.weatherIconID(cond); id != "" {
			ids[id] = true
		}
	}
	return ids
}

// pomodoroIconIDs returns the native icon IDs the Pomodoro payload can
// reference (tomato for focus, coffee for breaks; see render.PomodoroPayload),
// or nil if Pomodoro is disabled.
func pomodoroIconIDs(cfg PomodoroConfig) map[string]bool {
	if !cfg.Enabled {
		return nil
	}
	return map[string]bool{
		render.PomoFocusIconID: true,
		render.PomoBreakIconID: true,
	}
}

// lametricIconBaseURL is the LaMetric developer gallery's icon-thumbnail
// endpoint. Extracted so tests can point fetchIconFrom at a fake server.
const lametricIconBaseURL = "https://developer.lametric.com/content/apps/icon_thumbs/"

// fetchLaMetricIcon downloads an 8×8 gallery icon by ID from the LaMetric
// developer gallery. See fetchIconFrom for the fallback chain.
func fetchLaMetricIcon(ctx context.Context, id string) ([]byte, string, error) {
	return fetchIconFrom(ctx, lametricIconBaseURL, id)
}

// fetchIconFrom downloads a gallery icon by ID from baseURL, trying the
// animated .gif form first, then the static .jpg. Some gallery icons (e.g.
// 29802/tomato) have neither: as a last resort it fetches the extensionless
// URL, which serves a tiny PNG for those IDs. awtrix-ng's icon upload only
// accepts GIF/JPEG magic bytes, so a PNG fallback is decoded and re-encoded
// as GIF locally. Returns the bytes + extension.
func fetchIconFrom(ctx context.Context, baseURL, id string) ([]byte, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	for _, ext := range []string{"gif", "jpg"} {
		body, status, err := fetchBytes(ctx, client, baseURL+id+"."+ext)
		if err != nil {
			lastErr = err
			continue
		}
		if status != http.StatusOK || len(body) == 0 {
			lastErr = fmt.Errorf("icon %s.%s: status %d", id, ext, status)
			continue
		}
		return body, ext, nil
	}

	body, status, err := fetchBytes(ctx, client, baseURL+id)
	if err != nil || status != http.StatusOK || len(body) == 0 {
		return nil, "", lastErr
	}
	gifBytes, err := pngToGIF(body)
	if err != nil {
		return nil, "", fmt.Errorf("icon %s: fallback fetch not a usable image: %w", id, err)
	}
	return gifBytes, "gif", nil
}

func fetchBytes(ctx context.Context, client *http.Client, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

var pngMagic = []byte("\x89PNG")

// pngToGIF decodes a PNG and re-encodes it as GIF (stdlib only), so
// awtrix-ng's magic-byte check on /api/v1/files?dir=/ICONS (GIF or JPEG only,
// PNG rejected with 415) accepts it. Guards against converting anything that
// isn't a tiny gallery icon — e.g. an HTML error page — by requiring the PNG
// magic bytes and bounds no larger than 16×16.
func pngToGIF(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, pngMagic) {
		return nil, fmt.Errorf("not a PNG (magic bytes)")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	if b := img.Bounds(); b.Dx() > 16 || b.Dy() > 16 {
		return nil, fmt.Errorf("image %dx%d too large for a gallery icon", b.Dx(), b.Dy())
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		return nil, fmt.Errorf("encode gif: %w", err)
	}
	return buf.Bytes(), nil
}
