package main

import (
	"encoding/json"
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

const weatherSnapshotKey = "weather_snapshot"

type weatherSnapshotRecord struct {
	Provider     string              `json:"provider"`
	Model        string              `json:"model"`
	Lat          float64             `json:"lat"`
	Lon          float64             `json:"lon"`
	LastModified string              `json:"last_modified,omitempty"`
	Obs          weather.Observation `json:"obs"`
}

func (r weatherSnapshotRecord) source() weather.Source {
	return weather.Source{Provider: r.Provider, Model: r.Model, Loc: weather.Location{Lat: r.Lat, Lon: r.Lon}}
}

func (a *App) saveWeatherSnapshot(src weather.Source) {
	if a.store == nil {
		return
	}
	a.weather.mu.RLock()
	if !a.weather.have || a.weather.source != src {
		a.weather.mu.RUnlock()
		return
	}
	r := weatherSnapshotRecord{
		Provider:     src.Provider,
		Model:        src.Model,
		Lat:          src.Loc.Lat,
		Lon:          src.Loc.Lon,
		LastModified: a.weather.lastModified,
		Obs:          a.weather.obs,
	}
	a.weather.mu.RUnlock()
	b, err := json.Marshal(r)
	if err != nil {
		a.logger.Warn("weather snapshot encode failed", "err", err)
		return
	}
	if err := a.store.PutSetting(weatherSnapshotKey, string(b)); err != nil {
		a.logger.Warn("weather snapshot persist failed", "err", err)
	}
}

func (a *App) restoreWeatherSnapshot(src weather.Source, now time.Time) {
	a.weather.mu.Lock()
	if a.weather.snapshotLoaded || a.store == nil {
		a.weather.mu.Unlock()
		return
	}
	a.weather.snapshotLoaded = true
	a.weather.mu.Unlock()
	v, ok, err := a.store.GetSetting(weatherSnapshotKey)
	if err != nil {
		a.logger.Warn("weather snapshot read failed", "err", err)
		return
	}
	var r weatherSnapshotRecord
	if !ok || json.Unmarshal([]byte(v), &r) != nil || r.source() != src || r.Obs.FetchedAt.After(now) {
		return
	}
	if now.Sub(later(r.Obs.FetchedAt, r.Obs.ValidUntil)) >= weatherStaleAfter {
		return
	}
	a.weather.mu.Lock()
	if a.weather.have || a.weather.source != src {
		a.weather.mu.Unlock()
		return
	}
	a.weather.obs = r.Obs
	a.weather.have = true
	a.weather.lastFetch = r.Obs.FetchedAt
	a.weather.lastModified = r.LastModified
	a.weather.notBefore = r.Obs.ValidUntil
	a.weather.mu.Unlock()
	a.logger.Info("weather restored from store", "provider", src.Provider, "fetched_at", r.Obs.FetchedAt)
	a.changes.notify(topicWeather)
	a.nudgePomo()
}
