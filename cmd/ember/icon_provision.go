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

func (a *App) provisionIconsInBackground() {
	a.iconJobs.Go(func() { a.ensureNativeIcons(context.Background()) })
}

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

	if clockDisabled() {
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

func pomodoroIconIDs(cfg PomodoroConfig) map[string]bool {
	if !cfg.Enabled {
		return nil
	}
	return map[string]bool{
		render.PomoFocusIconID: true,
		render.PomoBreakIconID: true,
	}
}

const lametricIconBaseURL = "https://developer.lametric.com/content/apps/icon_thumbs/"

func fetchLaMetricIcon(ctx context.Context, id string) ([]byte, string, error) {
	return fetchIconFrom(ctx, lametricIconBaseURL, id)
}

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
