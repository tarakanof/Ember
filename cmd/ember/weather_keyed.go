package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

func (a *App) weatherUnavailableReason(cfg weather.Config) string {
	if cfg.Enabled && !a.weatherFetcher.ProviderAvailable(cfg.Provider) {
		return cfg.Provider + " needs " + weather.KeyedProviders[cfg.Provider].Env + " on the server"
	}
	return ""
}

type providerQuota struct {
	attemptAt    time.Time
	retryAt      time.Time
	savedRetryAt time.Time
	loaded       bool
	readFailing  bool
}

func (s *weatherStore) quota(provider string) *providerQuota {
	if s.quotas == nil {
		s.quotas = map[string]*providerQuota{}
	}
	q := s.quotas[provider]
	if q == nil {
		q = &providerQuota{}
		s.quotas[provider] = q
	}
	return q
}

const quotaTickSlack = 30 * time.Second

func (q *providerQuota) open(floor time.Duration, now time.Time) bool {
	if q == nil {
		return true
	}
	return !now.Before(q.retryAt) && (q.attemptAt.IsZero() || now.Sub(q.attemptAt) >= floor-quotaTickSlack)
}

func (s *weatherStore) reserveQuota(cfg weather.Config, now time.Time) bool {
	floor := cfg.MinFetchInterval()
	if floor == 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.quota(cfg.Provider)
	if !q.open(floor, now) {
		return false
	}
	q.attemptAt = now
	return true
}

func weatherQuotaKey(provider string) string {
	return "weather_quota_" + strings.ReplaceAll(provider, "-", "_")
}

type weatherQuotaRecord struct {
	AttemptAt  time.Time `json:"attempt_at"`
	RetryUntil time.Time `json:"retry_until"`
}

func (a *App) loadWeatherQuota(cfg weather.Config, now time.Time) {
	if cfg.MinFetchInterval() == 0 {
		return
	}
	a.weather.mu.Lock()
	defer a.weather.mu.Unlock()
	q := a.weather.quota(cfg.Provider)
	if q.loaded {
		return
	}
	if a.store == nil {
		q.loaded = true
		return
	}
	v, ok, err := a.store.GetSetting(weatherQuotaKey(cfg.Provider))
	if err != nil {
		if q.attemptAt.IsZero() {
			q.attemptAt = now
		}
		if !q.readFailing {
			a.logger.Warn("weather quota read failed, holding fetches for the minimum interval and retrying each poll", "provider", cfg.Provider, "err", err)
		}
		q.readFailing = true
		return
	}
	if q.readFailing {
		a.logger.Info("weather quota read recovered", "provider", cfg.Provider)
	}
	q.loaded, q.readFailing = true, false
	var r weatherQuotaRecord
	if !ok || json.Unmarshal([]byte(v), &r) != nil {
		return
	}
	if !r.AttemptAt.After(now) {
		q.attemptAt = later(q.attemptAt, r.AttemptAt)
	}
	until := earlier(r.RetryUntil, now.Add(weather.RetryAfterMax))
	q.retryAt = later(q.retryAt, until)
	q.savedRetryAt = later(q.savedRetryAt, until)
}

func (a *App) saveWeatherQuota(cfg weather.Config) {
	if a.store == nil || cfg.MinFetchInterval() == 0 {
		return
	}
	a.weather.mu.RLock()
	var r weatherQuotaRecord
	if q := a.weather.quotas[cfg.Provider]; q != nil {
		r = weatherQuotaRecord{AttemptAt: q.attemptAt, RetryUntil: q.savedRetryAt}
	}
	a.weather.mu.RUnlock()
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	if err := a.store.PutSetting(weatherQuotaKey(cfg.Provider), string(b)); err != nil {
		a.logger.Warn("weather quota persist failed", "provider", cfg.Provider, "err", err)
	}
}
