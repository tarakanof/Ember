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
	meetingsStaleTTL            = 60 * time.Minute // hide tile+popup if feeds go dark (a cancelled meeting must not ghost)
	meetingPopupGrace           = 2 * time.Minute  // covers a missed tick (reminders precedent)
	meetingPopupDurationSeconds = 30
)

// defaultMeetingChime is a short ascending RTTTL chime (TC001 piezo is RTTTL-only).
const defaultMeetingChime = "meet:d=8,o=6,b=160:c,e,g"

// meetingsStore holds the upcoming occurrences and popup bookkeeping.
// The coordinator reads it via next/fresh; the poller writes it.
type meetingsStore struct {
	mu          sync.RWMutex
	upcoming    []meetings.Occurrence // sorted by Start, within the horizon
	lastFetch   time.Time             // last attempt (due-gate; set on success AND failure)
	lastFetchOK time.Time             // last success (staleness guard)
	fired       map[string]struct{}   // popup dedupe: UID|start-RFC3339
}

func newMeetingsStore() *meetingsStore {
	return &meetingsStore{
		fired: make(map[string]struct{}),
	}
}

// next returns the first occurrence with Start.After(now).
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

// fresh reports whether the store has a recent successful fetch within meetingsStaleTTL.
func (s *meetingsStore) fresh(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.lastFetchOK.IsZero() && now.Sub(s.lastFetchOK) < meetingsStaleTTL
}

// lastOK returns the time of the last successful fetch (zero if never fetched).
func (s *meetingsStore) lastOK() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastFetchOK
}

// snapshot returns up to n future occurrences (Start.After(now)) as a copy,
// safe for the coordinator to read without holding the lock.
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

// ---- ICS fetcher ----

// icsFetcher performs HTTP calls to ICS feed URLs. The client and userAgent are
// injectable so tests can point at httptest servers.
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

// fetch downloads and returns raw ICS bytes from rawURL.
// Network / TLS errors are replaced with a generic message to prevent
// *url.Error (which embeds the secret URL) from leaking into logs.
func (f *icsFetcher) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("ics: bad url")
	}
	req.Header.Set("User-Agent", f.userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, errors.New("ics: request failed") // NOT err — *url.Error embeds the secret URL
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ics: http %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

// ---- poller ----

// StartMeetings runs the ICS polling loop until ctx is cancelled.
// It mirrors StartWeather exactly: guard on meetings != nil, 1-min ticker,
// initial poll for a prompt first tile.
func (a *App) StartMeetings(ctx context.Context) {
	if a.meetings == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	a.pollMeetings(ctx, time.Now()) // initial attempt so the tile appears promptly
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pollMeetings(ctx, time.Now())
		}
	}
}

// pollMeetings checks the popup on every tick and fetches when due.
// The `now` parameter makes the function deterministic under test.
func (a *App) pollMeetings(ctx context.Context, now time.Time) {
	cfg := a.cfg.Load().Meetings
	if !cfg.IsEnabled() || len(a.meetingsURLs) == 0 {
		return
	}

	// Due-gate on lastFetch (set on BOTH success and failure), not on whether
	// we have data: a failing feed must back off the full refresh interval
	// between attempts rather than retry every tick. lastFetch zero = never
	// attempted → fetch now.
	a.meetings.mu.RLock()
	due := a.meetings.lastFetch.IsZero() || now.Sub(a.meetings.lastFetch) >= meetingsRefreshInterval
	a.meetings.mu.RUnlock()

	if due {
		// Record the attempt time before fetching (on both success and failure
		// paths below) so a failing feed backs off a full interval.
		a.meetings.mu.Lock()
		a.meetings.lastFetch = now
		a.meetings.mu.Unlock()

		var lists [][]meetings.Occurrence
		anySuccess := false
		for i, u := range a.meetingsURLs {
			data, err := a.meetingsFetcher.fetch(ctx, u)
			if err != nil {
				// Log by index only — never log the URL (it's a credential).
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

		// At least one feed succeeded (even with zero occurrences): replace
		// upcoming wholesale so a genuinely empty calendar clears the store.
		// On all-failure: keep previous upcoming, do not advance lastFetchOK.
		if anySuccess {
			merged := meetings.Merge(lists...)
			a.meetings.mu.Lock()
			a.meetings.upcoming = merged
			a.meetings.lastFetchOK = now
			// Prune fired entries whose embedded start is more than 2h before now.
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

	// Popup check runs EVERY tick (timing is minute-granular; fetches are 5-min).
	// On due ticks it runs AFTER the fetch so the popup sees fresh data — a
	// cancelled/moved meeting in the just-arriving ICS update cannot fire from
	// the previous snapshot. On non-due ticks it runs against the existing store.
	a.checkMeetingPopup(ctx, now, cfg)
}

// checkMeetingPopup fires a T-minus notification for every upcoming occurrence
// whose lead window [start−lead, start−lead+grace) contains now. Using a
// snapshot of up to 10 future occurrences rather than just the first (next())
// ensures back-to-back meetings — and any two meetings whose windows overlap
// the same tick — are each notified. Capping at 10 is safe: having more than
// 10 distinct meetings inside a single lead window (max 60 min) is not a real
// calendar scenario, and the fired-map dedupe guarantees each fires at most once.
func (a *App) checkMeetingPopup(ctx context.Context, now time.Time, cfg MeetingsConfig) {
	if cfg.PopupLeadMins() <= 0 || !a.meetings.fresh(now) {
		return
	}
	upcoming := a.meetings.snapshot(now, 10)
	if len(upcoming) == 0 {
		return
	}

	lead := time.Duration(cfg.PopupLeadMins()) * time.Minute

	// Single timeout context shared across all popups in this tick.
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, occ := range upcoming {
		fireAt := occ.Start.Add(-lead)
		if now.Before(fireAt) || now.Sub(fireAt) >= meetingPopupGrace {
			continue
		}
		key := occ.UID + "|" + occ.Start.UTC().Format(time.RFC3339)

		// Mark-before-fire under the store lock: prevents a double popup if two
		// goroutines (unlikely but possible on startup) race to the same window.
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
			// The chime rides on the notification; quietPublisher strips it at night.
			payload["soundRtttl"] = defaultMeetingChime
		}
		if err := a.publisher.Notify(cctx, payload); err != nil {
			a.logger.Warn("meeting popup failed", "err", err)
		}
	}
}

// sanitizeMeetingTitle returns an uppercase version of s suitable for the
// AWTRIX clock display: only [A-Z0-9 .,:%°/-] runes are kept; unsupported
// runes are dropped; space runs are collapsed; the result is trimmed and
// capped at 24 runes. An empty result falls back to "MEETING".
func sanitizeMeetingTitle(s string) string {
	s = strings.ToUpper(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // treat leading as space to avoid leading space after trim
	for _, r := range s {
		allowed := (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			strings.ContainsRune(" .,:%°/-", r)
		if !allowed {
			// Drop unsupported rune; if it was adjacent to text we may have
			// created a space run — handled below.
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
	// Cap at 24 runes, then strip any trailing space the cap may have exposed
	// (e.g. when the 24th rune is a space that TrimSpace above didn't see yet).
	if utf8.RuneCountInString(result) > 24 {
		runes := []rune(result)
		result = strings.TrimRight(string(runes[:24]), " ")
	}
	if result == "" {
		return "MEETING"
	}
	return result
}
