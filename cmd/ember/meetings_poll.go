package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/meetings"
	"github.com/tarakanof/ember/internal/render"
)

const (
	meetingsRefreshInterval     = 5 * time.Minute
	meetingsHorizon             = 36 * time.Hour
	meetingsStaleTTL            = 60 * time.Minute
	meetingPopupGrace           = 2 * time.Minute
	meetingPopupDurationSeconds = 30
)

const defaultMeetingChime = "meet:d=8,o=6,b=160:c,e,g"

type meetingsStore struct {
	mu          sync.RWMutex
	upcoming    []meetings.Occurrence
	lastFetch   time.Time
	lastFetchOK time.Time
	fired       map[string]struct{}
}

func newMeetingsStore() *meetingsStore {
	return &meetingsStore{
		fired: make(map[string]struct{}),
	}
}

func (s *meetingsStore) next(now time.Time) (meetings.Occurrence, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, occ := range s.upcoming {
		if occ.Start.After(now) {
			return occ, true
		}
	}
	return meetings.Occurrence{}, false
}

func (s *meetingsStore) fresh(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.lastFetchOK.IsZero() && now.Sub(s.lastFetchOK) < meetingsStaleTTL
}

func (s *meetingsStore) lastOK() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastFetchOK
}

func (s *meetingsStore) snapshot(now time.Time, n int) []meetings.Occurrence {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []meetings.Occurrence
	for _, occ := range s.upcoming {
		if occ.Start.After(now) {
			result = append(result, occ)
			if len(result) >= n {
				break
			}
		}
	}
	return result
}

type icsFetcher struct {
	client    *http.Client
	userAgent string
}

func newICSFetcher() *icsFetcher {
	return &icsFetcher{
		client:    &http.Client{Timeout: 12 * time.Second},
		userAgent: "ember-meetings/0.1 (github.com/tarakanof/ember)",
	}
}

func (f *icsFetcher) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("ics: bad url")
	}
	req.Header.Set("User-Agent", f.userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, errors.New("ics: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ics: http %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

// StartMeetings runs the ICS polling loop until ctx is cancelled.
func (a *App) StartMeetings(ctx context.Context) {
	if a.meetings == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	a.pollMeetings(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pollMeetings(ctx, time.Now())
		}
	}
}

func (a *App) pollMeetings(ctx context.Context, now time.Time) {
	cfg := a.cfg.Load().Meetings
	if !cfg.IsEnabled() || len(a.meetingsURLs) == 0 {
		return
	}

	a.meetings.mu.RLock()
	due := a.meetings.lastFetch.IsZero() || now.Sub(a.meetings.lastFetch) >= meetingsRefreshInterval
	a.meetings.mu.RUnlock()

	if due {
		a.meetings.mu.Lock()
		a.meetings.lastFetch = now
		a.meetings.mu.Unlock()

		var lists [][]meetings.Occurrence
		anySuccess := false
		for i, u := range a.meetingsURLs {
			data, err := a.meetingsFetcher.fetch(ctx, u)
			if err != nil {
				a.logger.Warn("meetings fetch failed", "url_index", i, "err", err)
				continue
			}
			occs, err := meetings.Expand(data, now, meetingsHorizon)
			if err != nil {
				a.logger.Warn("meetings parse failed", "url_index", i, "err", err)
				continue
			}
			lists = append(lists, occs)
			anySuccess = true
		}

		if anySuccess {
			merged := meetings.Merge(lists...)
			a.meetings.mu.Lock()
			a.meetings.upcoming = merged
			a.meetings.lastFetchOK = now
			for key := range a.meetings.fired {
				pipe := strings.LastIndex(key, "|")
				if pipe < 0 {
					continue
				}
				startStr := key[pipe+1:]
				startTime, err := time.Parse(time.RFC3339, startStr)
				if err != nil {
					continue
				}
				if now.Sub(startTime) > 2*time.Hour {
					delete(a.meetings.fired, key)
				}
			}
			a.meetings.mu.Unlock()
		}

		a.nudgePomo()
	}

	a.checkMeetingPopup(ctx, now, cfg)
}

func (a *App) checkMeetingPopup(ctx context.Context, now time.Time, cfg MeetingsConfig) {
	if cfg.PopupLeadMins() <= 0 || !a.meetings.fresh(now) {
		return
	}
	upcoming := a.meetings.snapshot(now, 10)
	if len(upcoming) == 0 {
		return
	}

	lead := time.Duration(cfg.PopupLeadMins()) * time.Minute

	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, occ := range upcoming {
		fireAt := occ.Start.Add(-lead)
		if now.Before(fireAt) || now.Sub(fireAt) >= meetingPopupGrace {
			continue
		}
		key := occ.UID + "|" + occ.Start.UTC().Format(time.RFC3339)

		a.meetings.mu.Lock()
		if _, done := a.meetings.fired[key]; done {
			a.meetings.mu.Unlock()
			continue
		}
		a.meetings.fired[key] = struct{}{}
		a.meetings.mu.Unlock()

		payload := render.MeetingPopupPayload(sanitizeMeetingTitle(occ.Title), cfg.PopupLeadMins(), meetingPopupDurationSeconds)
		payload["name"] = notifyNameMeeting
		if cfg.ChimeEnabled() {
			payload["soundRtttl"] = defaultMeetingChime
		}
		if err := a.publisher.Notify(cctx, payload); err != nil {
			a.logger.Warn("meeting popup failed", "err", err)
		}
	}
}

func sanitizeMeetingTitle(s string) string {
	s = strings.ToUpper(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true
	for _, r := range s {
		allowed := (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			strings.ContainsRune(" .,:%°/-", r)
		if !allowed {
			if unicode.IsSpace(r) && !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		if r == ' ' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	result := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(result) > 24 {
		runes := []rune(result)
		result = strings.TrimRight(string(runes[:24]), " ")
	}
	if result == "" {
		return "MEETING"
	}
	return result
}
