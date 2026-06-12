package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// ensureWeatherIcons makes sure every native icon ID the weather config can
// reference (the six condition buckets, overrides included) exists in the
// clock's /ICONS folder, downloading missing ones from the LaMetric gallery
// and uploading them to the device. The device's own on-demand downloads are
// unreliable (observed failing for hours), so the server provisions instead.
//
// A no-op unless weather is enabled with a native-icon toggle on. List
// failures abort the run (no blind re-uploads); per-icon failures are logged
// and skipped — the next config apply or restart retries. Safe to call from
// any goroutine; runs are serialised by iconMu.
func (a *App) ensureWeatherIcons(ctx context.Context) {
	cfg := a.cfg.Load().Weather
	if !cfg.Enabled || (!cfg.UseNativeIcons && !cfg.TileNativeIcons) {
		return
	}
	a.iconMu.Lock()
	defer a.iconMu.Unlock()

	need := map[string]bool{}
	for _, cond := range []string{
		render.WeatherClear, render.WeatherClouds, render.WeatherFog,
		render.WeatherRain, render.WeatherSnow, render.WeatherStorm,
	} {
		if id := cfg.weatherIconID(cond); id != "" {
			need[id] = true
		}
	}
	if len(need) == 0 {
		return
	}

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

// fetchLaMetricIcon downloads an 8×8 gallery icon by ID, trying the animated
// .gif form first, then the static .jpg. Returns the bytes + extension.
func fetchLaMetricIcon(ctx context.Context, id string) ([]byte, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	for _, ext := range []string{"gif", "jpg"} {
		url := "https://developer.lametric.com/content/apps/icon_thumbs/" + id + "." + ext
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			lastErr = fmt.Errorf("icon %s.%s: status %d", id, ext, resp.StatusCode)
			continue
		}
		return body, ext, nil
	}
	return nil, "", lastErr
}
