package weather

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
)

type keyedProvider struct {
	Env         string
	minInterval time.Duration
}

var KeyedProviders = map[string]keyedProvider{
	TomorrowIO: {Env: TomorrowKeyEnv, minInterval: 5 * time.Minute},
	WeatherAPI: {Env: WeatherAPIKeyEnv, minInterval: 5 * time.Minute},
}

func (wf *Fetcher) Key(provider string) string {
	switch provider {
	case TomorrowIO:
		return wf.TomorrowKey
	case WeatherAPI:
		return wf.WeatherAPIKey
	}
	return ""
}

func (wf *Fetcher) ProviderAvailable(provider string) bool {
	_, keyed := KeyedProviders[provider]
	return !keyed || wf.Key(provider) != ""
}

func (wf *Fetcher) ProvidersAvailable() []string {
	out := make([]string, 0, len(Providers))
	for _, p := range Providers {
		if wf.ProviderAvailable(p) {
			out = append(out, p)
		}
	}
	return out
}

func keyedFetchError(ctx context.Context, err error, now time.Time, errRequest, errResponse error) error {
	var he *HTTPError
	if errors.As(err, &he) {
		ra := he.RetryAfter
		switch he.Code {
		case http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusForbidden:
			if ra == 0 {
				ra = now.Truncate(time.Hour).Add(time.Hour).Sub(now)
			}
		}
		return &HTTPError{Code: he.Code, Status: strconv.Itoa(he.Code), RetryAfter: ra}
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
