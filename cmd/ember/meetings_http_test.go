package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
	"github.com/tarakanof/ember/internal/render"
)

// seedMeetingsStore injects future occurrences directly into a meetingsStore.
// lastOK, if non-zero, marks the store as freshly fetched.
func seedMeetingsStore(s *meetingsStore, occs []meetings.Occurrence, lastOK time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upcoming = occs
	s.lastFetchOK = lastOK
}

func TestMeetingsPreviewFallback(t *testing.T) {
	// An app with an empty store (no meetings) must always return a
	// valid Preview with exactly 1 frame, card "meeting", and non-empty
	// pixels that contain at least one non-"000000" entry.
	a := newTestAppWithStore(t)

	w := httptest.NewRecorder()
	a.handleMeetingsPreview(w, httptest.NewRequest("GET", "/v1/meetings/preview", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body)
	}

	var p render.Preview
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Width != 32 || p.Height != 8 {
		t.Errorf("dimensions: got %dx%d, want 32x8", p.Width, p.Height)
	}
	if len(p.Frames) != 1 {
		t.Fatalf("frames: got %d, want 1", len(p.Frames))
	}
	f := p.Frames[0]
	if f.Card != "meeting" {
		t.Errorf("card: got %q, want \"meeting\"", f.Card)
	}
	if len(f.Pixels) == 0 {
		t.Fatal("pixels: empty")
	}
	hasColor := false
	for _, px := range f.Pixels {
		if px != "#000000" {
			hasColor = true
			break
		}
	}
	if !hasColor {
		t.Error("pixels: all black — sample render must produce at least one non-black pixel")
	}
}

func TestMeetingsPreviewLive(t *testing.T) {
	// A store with a future meeting and a fresh lastFetchOK must produce
	// a different frame than the fallback (all-empty-store) path.
	now := time.Now()
	a := newTestAppWithStore(t)

	// First capture fallback pixels.
	wFallback := httptest.NewRecorder()
	a.handleMeetingsPreview(wFallback, httptest.NewRequest("GET", "/v1/meetings/preview", nil))
	var fallback render.Preview
	if err := json.NewDecoder(wFallback.Body).Decode(&fallback); err != nil {
		t.Fatalf("decode fallback: %v", err)
	}

	// Seed the store with a future meeting and mark as freshly fetched.
	seedMeetingsStore(a.meetings, []meetings.Occurrence{
		{UID: "live@test", Title: "TEAM SYNC", Start: now.Add(5 * time.Minute)},
	}, now)

	wLive := httptest.NewRecorder()
	a.handleMeetingsPreview(wLive, httptest.NewRequest("GET", "/v1/meetings/preview", nil))

	if wLive.Code != http.StatusOK {
		t.Fatalf("live status = %d; body = %s", wLive.Code, wLive.Body)
	}
	var live render.Preview
	if err := json.NewDecoder(wLive.Body).Decode(&live); err != nil {
		t.Fatalf("decode live: %v", err)
	}
	if len(live.Frames) != 1 {
		t.Fatalf("live frames: got %d, want 1", len(live.Frames))
	}

	// The pixel arrays must differ (different title renders differently).
	if slicesEqual(live.Frames[0].Pixels, fallback.Frames[0].Pixels) {
		t.Error("live and fallback frames must differ — live path should render TEAM SYNC, not STANDUP 12m")
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMeetingsPreviewOpen(t *testing.T) {
	// Both /v1/meetings/preview and /v1/meetings/state must be reachable
	// without a Bearer token (open mux).
	_, srv := newTestServer(t, defaultConfig())

	for _, path := range []string{"/v1/meetings/preview", "/v1/meetings/state"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s without token: got %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestMeetingsState(t *testing.T) {
	// A store with 7 future + 1 past occurrence must return exactly 5
	// upcoming entries (capped), first entry matches, timestamps are
	// RFC3339 truncated to whole seconds (no fractional part).
	now := time.Now().UTC().Truncate(time.Second)

	a := newTestAppWithStore(t)

	var occs []meetings.Occurrence
	// 1 past occurrence (must be excluded).
	occs = append(occs, meetings.Occurrence{
		UID: "past@test", Title: "PAST MEETING",
		Start: now.Add(-1 * time.Hour),
	})
	// 7 future occurrences.
	for i := 0; i < 7; i++ {
		occs = append(occs, meetings.Occurrence{
			UID:   "future@test",
			Title: "FUTURE MEETING",
			Start: now.Add(time.Duration(i+1) * time.Hour),
		})
	}
	seedMeetingsStore(a.meetings, occs, now)

	w := httptest.NewRecorder()
	a.handleMeetingsState(w, httptest.NewRequest("GET", "/v1/meetings/state", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body)
	}

	var got struct {
		Upcoming []struct {
			Title string `json:"title"`
			Start string `json:"start"`
		} `json:"upcoming"`
		FetchedAt *string `json:"fetched_at,omitempty"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Upcoming) != 5 {
		t.Fatalf("upcoming count: got %d, want 5", len(got.Upcoming))
	}
	if got.Upcoming[0].Title != "FUTURE MEETING" {
		t.Errorf("first title: got %q, want %q", got.Upcoming[0].Title, "FUTURE MEETING")
	}
	expectedStart := now.Add(time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	if got.Upcoming[0].Start != expectedStart {
		t.Errorf("first start: got %q, want %q", got.Upcoming[0].Start, expectedStart)
	}
	for i, item := range got.Upcoming {
		if strings.Contains(item.Start, ".") {
			t.Errorf("upcoming[%d].start %q contains fractional seconds — Swift .iso8601 will reject it", i, item.Start)
		}
	}
	if got.FetchedAt == nil {
		t.Error("fetched_at must be present when store has a successful fetch")
	} else if strings.Contains(*got.FetchedAt, ".") {
		t.Errorf("fetched_at %q contains fractional seconds", *got.FetchedAt)
	}
	// Past occurrence must not appear.
	for _, item := range got.Upcoming {
		if item.Title == "PAST MEETING" {
			t.Error("past occurrence must not appear in upcoming")
		}
	}
}

func TestMeetingsStateUID(t *testing.T) {
	// Two occurrences with the SAME title and SAME start but DIFFERENT UIDs must
	// both appear in the response and each must carry its distinct uid field.
	// This guards against the same-title/same-start aliasing bug.
	now := time.Now().UTC().Truncate(time.Second)

	a := newTestAppWithStore(t)

	occs := []meetings.Occurrence{
		{UID: "alpha@feed1", Title: "STANDUP", Start: now.Add(time.Hour)},
		{UID: "beta@feed2", Title: "STANDUP", Start: now.Add(time.Hour)},
	}
	seedMeetingsStore(a.meetings, occs, now)

	w := httptest.NewRecorder()
	a.handleMeetingsState(w, httptest.NewRequest("GET", "/v1/meetings/state", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body)
	}

	var got struct {
		Upcoming []struct {
			Title string `json:"title"`
			Start string `json:"start"`
			UID   string `json:"uid"`
		} `json:"upcoming"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Upcoming) != 2 {
		t.Fatalf("upcoming count: got %d, want 2 (both occurrences with same title+start but different UIDs)", len(got.Upcoming))
	}

	uids := map[string]bool{}
	for i, item := range got.Upcoming {
		if item.UID == "" {
			t.Errorf("upcoming[%d]: uid is empty — uid must be present in state payload", i)
		}
		if item.Title != "STANDUP" {
			t.Errorf("upcoming[%d].title: got %q, want STANDUP", i, item.Title)
		}
		if strings.Contains(item.Start, ".") {
			t.Errorf("upcoming[%d].start %q contains fractional seconds", i, item.Start)
		}
		// No ICS URL must appear — uid is an event ID, not a feed URL.
		if strings.Contains(item.UID, "://") {
			t.Errorf("upcoming[%d].uid %q looks like a URL — must not expose feed URLs", i, item.UID)
		}
		uids[item.UID] = true
	}
	if !uids["alpha@feed1"] {
		t.Error("uid alpha@feed1 not found in response")
	}
	if !uids["beta@feed2"] {
		t.Error("uid beta@feed2 not found in response")
	}
}

func TestMeetingsStateEmpty(t *testing.T) {
	// An empty store must return {"upcoming":[]} (NOT null) and no fetched_at.
	a := newTestAppWithStore(t)

	w := httptest.NewRecorder()
	a.handleMeetingsState(w, httptest.NewRequest("GET", "/v1/meetings/state", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body)
	}

	body, _ := io.ReadAll(w.Body)
	bodyStr := string(body)

	// upcoming must be [] not null.
	if !strings.Contains(bodyStr, `"upcoming":[]`) {
		t.Errorf("empty store must marshal upcoming as [], got: %s", bodyStr)
	}
	// fetched_at must be absent (zero time → omitempty).
	if strings.Contains(bodyStr, "fetched_at") {
		t.Errorf("empty store must not include fetched_at, got: %s", bodyStr)
	}
}

func TestDoctorMeetingsCheck(t *testing.T) {
	t.Run("no URLs → OK not configured", func(t *testing.T) {
		a := newTestAppWithStore(t)
		a.meetingsURLs = nil
		cfg := a.cfg.Load()
		res := checkMeetings(a, cfg)
		if res.Status != StatusOK {
			t.Errorf("status: got %q, want OK", res.Status)
		}
		if !strings.Contains(res.Detail, "not configured") {
			t.Errorf("detail must mention 'not configured': %q", res.Detail)
		}
	})

	t.Run("URLs + never fetched → WARN", func(t *testing.T) {
		a := newTestAppWithStore(t)
		a.meetingsURLs = []string{"https://secret.example.com/feed.ics"}
		cfg := a.cfg.Load()
		cfg.Meetings.Enabled = boolPtr(true) // poller is active; never fetched is genuinely broken
		res := checkMeetings(a, cfg)
		if res.Status != StatusWarn {
			t.Errorf("status: got %q, want WARN", res.Status)
		}
		// URL must never appear in detail.
		if strings.Contains(res.Detail, "secret.example.com") {
			t.Errorf("detail must not contain the URL: %q", res.Detail)
		}
	})

	t.Run("URLs + fresh fetch + upcoming meeting → OK", func(t *testing.T) {
		now := time.Now()
		a := newTestAppWithStore(t)
		a.meetingsURLs = []string{"https://a.example/a.ics", "https://b.example/b.ics"}
		seedMeetingsStore(a.meetings, []meetings.Occurrence{
			{UID: "x@test", Title: "STANDUP", Start: now.Add(10 * time.Minute)},
		}, now)
		cfg := a.cfg.Load()
		res := checkMeetings(a, cfg)
		if res.Status != StatusOK {
			t.Errorf("status: got %q, want OK; detail: %s", res.Status, res.Detail)
		}
		// Must mention feed count.
		if !strings.Contains(res.Detail, "2 feed") {
			t.Errorf("detail must mention feed count '2 feed': %q", res.Detail)
		}
		// Must not contain any URL.
		for _, url := range a.meetingsURLs {
			if strings.Contains(res.Detail, url) {
				t.Errorf("detail must not contain URL %q: got %q", url, res.Detail)
			}
		}
		// Must not contain URL substrings.
		if strings.Contains(res.Detail, "a.example") || strings.Contains(res.Detail, "b.example") {
			t.Errorf("detail must not contain URL hostnames: %q", res.Detail)
		}
	})
}
