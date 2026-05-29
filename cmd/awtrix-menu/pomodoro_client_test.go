package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPomodoroClientStateAndStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/pomodoro/state":
			json.NewEncoder(w).Encode(pomoState{Phase: "focus", Running: true, RemainingSec: 1490, PlannedSec: 1500})
		case "/v1/pomodoro/stats":
			json.NewEncoder(w).Encode(pomoStats{Today: pomoDayStat{CompletedFocus: 3, FocusMin: 75}, Streak: 2})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newPomodoroClient(srv.URL, "")
	st, err := c.State()
	if err != nil || st.Phase != "focus" || st.RemainingSec != 1490 {
		t.Fatalf("State = %+v, err=%v", st, err)
	}
	s, err := c.Stats()
	if err != nil || s.Today.CompletedFocus != 3 || s.Streak != 2 {
		t.Fatalf("Stats = %+v, err=%v", s, err)
	}
}

func TestPomodoroClientActionSendsTokenToCorrectPath(t *testing.T) {
	var gotPath, gotAuth, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newPomodoroClient(srv.URL, "secret")
	if err := c.Action("pause"); err != nil {
		t.Fatalf("Action: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/pomodoro/pause" {
		t.Fatalf("method/path = %s %s, want POST /v1/pomodoro/pause", gotMethod, gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("auth = %q, want Bearer secret", gotAuth)
	}
}

func TestPomodoroClientActionRejectsUnknown(t *testing.T) {
	c := newPomodoroClient("http://example", "")
	if err := c.Action("explode"); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestPomodoroClientConfigRoundTrip(t *testing.T) {
	var stored pomoConfig
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			json.NewDecoder(r.Body).Decode(&stored)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			json.NewEncoder(w).Encode(stored)
		}
	}))
	defer srv.Close()

	c := newPomodoroClient(srv.URL, "tok")
	want := pomoConfig{FocusMinutes: 30, ShortBreakMinutes: 5, LongBreakMinutes: 15, RoundsBeforeLongBreak: 4, AutoStartNext: true, Sound: true, FocusColor: "#FF3B30", BreakColor: "#2EE85E"}
	if err := c.PutConfig(want); err != nil {
		t.Fatalf("PutConfig: %v", err)
	}
	got, err := c.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got != want {
		t.Fatalf("config round-trip got %+v want %+v", got, want)
	}
}

func TestPomodoroClientErrorsWithoutURL(t *testing.T) {
	c := newPomodoroClient("", "")
	if _, err := c.State(); err == nil {
		t.Fatal("expected error with no server URL")
	}
}
