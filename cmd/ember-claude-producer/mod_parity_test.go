package main

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type modParity struct {
	Activity []struct {
		Tool  string          `json:"tool"`
		Input json.RawMessage `json:"input"`
		Want  string          `json:"want"`
	} `json:"activity"`
	StopFailure []struct {
		Error, Details, Last, Want string
	} `json:"stop_failure"`
	FailureOutcome []struct {
		Error     string `json:"error"`
		Interrupt bool   `json:"interrupt"`
		Want      string `json:"want"`
	} `json:"failure_outcome"`
	PrependTrail []struct {
		Head, Prev, Want string
	} `json:"prepend_trail"`
	AnnotateTrail []struct {
		Trail, Item, Annotated string
		Prepend                bool
		Want                   string
	} `json:"annotate_trail"`
	EnvFile []struct {
		Text string            `json:"text"`
		Want map[string]string `json:"want"`
	} `json:"env_file"`
	SessionID []struct {
		Raw, Cwd, Want string
	} `json:"session_id"`
}

func readModParity(t *testing.T) modParity {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, "producers", "claude-code", "mod", "test", "parity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p modParity
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("parity.json: %v", err)
	}
	return p
}

func TestModParityActivity(t *testing.T) {
	for _, c := range readModParity(t).Activity {
		if got := activityString(c.Tool, c.Input); got != c.Want {
			t.Errorf("activityString(%q, %s) = %q, fixture says %q", c.Tool, c.Input, got, c.Want)
		}
	}
}

func TestModParityStopFailure(t *testing.T) {
	for _, c := range readModParity(t).StopFailure {
		in := hookInput{Error: c.Error, ErrorDetails: c.Details, LastAssistantMessage: c.Last}
		if got := stopFailureMessage(in); got != c.Want {
			t.Errorf("stopFailureMessage(%+v) = %q, fixture says %q", in, got, c.Want)
		}
	}
}

func TestModParityFailureOutcome(t *testing.T) {
	for _, c := range readModParity(t).FailureOutcome {
		in := hookInput{Error: c.Error, IsInterrupt: c.Interrupt}
		if got := failureOutcome(in); got != c.Want {
			t.Errorf("failureOutcome(%+v) = %q, fixture says %q", in, got, c.Want)
		}
	}
}

func TestModParityTrail(t *testing.T) {
	p := readModParity(t)
	for _, c := range p.PrependTrail {
		if got := producer.PrependTrail(c.Head, c.Prev); got != c.Want {
			t.Errorf("PrependTrail(%q, %q) = %q, fixture says %q", c.Head, c.Prev, got, c.Want)
		}
	}
	for _, c := range p.AnnotateTrail {
		if got := producer.AnnotateTrail(c.Trail, c.Item, c.Annotated, c.Prepend); got != c.Want {
			t.Errorf("AnnotateTrail(%q, %q, %q, %v) = %q, fixture says %q", c.Trail, c.Item, c.Annotated, c.Prepend, got, c.Want)
		}
	}
}

func TestModParityEnvFile(t *testing.T) {
	for _, c := range readModParity(t).EnvFile {
		path := filepath.Join(t.TempDir(), "producer.env")
		if err := os.WriteFile(path, []byte(c.Text), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := producer.ReadEnvFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(got, c.Want) {
			t.Errorf("ReadEnvFile(%q) = %v, fixture says %v", c.Text, got, c.Want)
		}
	}
}

func TestModParitySessionID(t *testing.T) {
	for _, c := range readModParity(t).SessionID {
		if got := sanitizeSessionID(c.Raw, c.Cwd); got != c.Want {
			t.Errorf("sanitizeSessionID(%q, %q) = %q, fixture says %q", c.Raw, c.Cwd, got, c.Want)
		}
	}
}

type wireCall struct {
	Method string         `json:"method"`
	Body   map[string]any `json:"body"`
}

type modTransitions struct {
	Env       string `json:"env"`
	Scenarios []struct {
		Name  string `json:"name"`
		Steps []struct {
			At    int64           `json:"at"`
			Event string          `json:"event"`
			Input json.RawMessage `json:"input"`
			Want  []wireCall      `json:"want"`
		} `json:"steps"`
	} `json:"scenarios"`
}

func TestModParityTransitions(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "producers", "claude-code", "mod", "test", "transitions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx modTransitions
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("transitions.json: %v", err)
	}
	for _, sc := range fx.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			var mu sync.Mutex
			var got []wireCall
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				c := wireCall{Method: r.Method}
				if err := json.Unmarshal(b, &c.Body); err != nil {
					t.Errorf("%s body %q: %v", r.Method, b, err)
				}
				mu.Lock()
				got = append(got, c)
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", "")
			t.Setenv("EMBER_TOKEN", "")
			cfgDir := filepath.Join(home, ".config", "ember")
			if err := os.MkdirAll(cfgDir, 0o700); err != nil {
				t.Fatal(err)
			}
			env := fx.Env + "EMBER_SERVER_URL=" + srv.URL + "\n"
			if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
				t.Fatal(err)
			}
			stubOwner(t)
			base := time.Now()
			var at time.Duration
			orig := hookNow
			hookNow = func() time.Time { return base.Add(at) }
			t.Cleanup(func() { hookNow = orig })
			cfg, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			for i, step := range sc.Steps {
				at = time.Duration(step.At) * time.Second
				mu.Lock()
				got = nil
				mu.Unlock()
				if step.Event == "tick" {
					dispatchTick(context.Background(), cfg)
				} else {
					dispatchHook(context.Background(), step.Event, step.Input, cfg)
				}
				mu.Lock()
				calls := append([]wireCall{}, got...)
				mu.Unlock()
				want := step.Want
				if want == nil {
					want = []wireCall{}
				}
				if len(calls) == 0 {
					calls = []wireCall{}
				}
				if !reflect.DeepEqual(calls, want) {
					t.Errorf("step %d (%s at %ds):\n got  %v\n want %v", i, step.Event, step.At, calls, want)
				}
			}
		})
	}
}
