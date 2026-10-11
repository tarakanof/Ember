package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type keyedProvider struct {
	env         string
	minInterval time.Duration
}

var keyedProviders = map[string]keyedProvider{
	tomorrowIO: {env: tomorrowKeyEnv, minInterval: 5 * time.Minute},
	weatherAPI: {env: weatherAPIKeyEnv, minInterval: 5 * time.Minute},
}

func (wf *weatherFetcher) key(provider string) string {
	switch provider {
	case tomorrowIO:
		return wf.tomorrowKey
	case weatherAPI:
		return wf.weatherAPIKey
	}
	return ""
}

func (wf *weatherFetcher) providerAvailable(provider string) bool {
	_, keyed := keyedProviders[provider]
	return !keyed || wf.key(provider) != ""
}

func (a *App) weatherUnavailableReason(cfg WeatherConfig) string {
	if cfg.Enabled && !a.weatherFetcher.providerAvailable(cfg.Provider) {
		return cfg.Provider + " needs " + keyedProviders[cfg.Provider].env + " on the server"
	}
	return ""
}

func (wf *weatherFetcher) providersAvailable() []string {
	out := make([]string, 0, len(weatherProviders))
	for _, p := range weatherProviders {
		if wf.providerAvailable(p) {
			out = append(out, p)
		}
	}
	return out
}

func keyedFetchError(ctx context.Context, err error, now time.Time, errRequest, errResponse error) error {
	var he *weatherHTTPError
	if errors.As(err, &he) {
		ra := he.retryAfter
		switch he.code {
		case http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusForbidden:
			if ra == 0 {
				ra = now.Truncate(time.Hour).Add(time.Hour).Sub(now)
			}
		}
		return &weatherHTTPError{code: he.code, status: strconv.Itoa(he.code), retryAfter: ra}
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %w", errRequest, ctx.Err())
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return errRequest
	}
	return errResponse
}

var wmoSeverity = []int{
	0, 1, 2, 3,
	45, 48,
	51, 53, 55, 56, 57, 61, 80, 66, 63, 81, 65, 82, 67,
	71, 85, 77, 73, 75, 86,
	95, 96, 99,
}

func valueOrInt(v *int, def int) int {
	if v == nil {
		return def
	}
	return *v
}

func pickDayCode(hours map[int]int) *int {
	var best *int
	bestScore, bestRank := -1, -1
	for code, h := range hours {
		rank := slices.Index(wmoSeverity, code)
		if rank < 0 {
			continue
		}
		if score := (rank + 1) * h; score > bestScore || (score == bestScore && rank > bestRank) {
			best, bestScore, bestRank = new(code), score, rank
		}
	}
	return best
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

func (s *weatherStore) reserveQuota(cfg WeatherConfig, now time.Time) bool {
	floor := cfg.minFetchInterval()
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

func (a *App) loadWeatherQuota(cfg WeatherConfig, now time.Time) {
	if cfg.minFetchInterval() == 0 {
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
	until := earlier(r.RetryUntil, now.Add(weatherRetryAfterMax))
	q.retryAt = later(q.retryAt, until)
	q.savedRetryAt = later(q.savedRetryAt, until)
}

func (a *App) saveWeatherQuota(cfg WeatherConfig) {
	if a.store == nil || cfg.minFetchInterval() == 0 {
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
