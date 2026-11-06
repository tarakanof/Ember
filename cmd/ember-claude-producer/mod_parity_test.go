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
	"strconv"
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
	HostName []struct {
		Raw, Want string
	} `json:"host_name"`
	AgentsParse []struct {
		Name string            `json:"name"`
		Text string            `json:"text"`
		Want map[string]string `json:"want"`
	} `json:"agents_parse"`
	AgentsWait []struct {
		Name    string `json:"name"`
		State   string `json:"state"`
		Message string `json:"message"`
		Pending bool   `json:"pending"`
		Status  string `json:"status"`
		Want    *struct {
			State   string `json:"state"`
			Message string `json:"message"`
		} `json:"want"`
	} `json:"agents_wait"`
	T3 []struct {
		Name  string            `json:"name"`
		Env   map[string]string `json:"env"`
		Procs map[string]struct {
			Ppid int      `json:"ppid"`
			Comm string   `json:"comm"`
			Argv []string `json:"argv"`
		} `json:"procs"`
		Want bool `json:"want"`
	} `json:"t3"`
	Gauges []struct {
		Name       string            `json:"name"`
		TZ         string            `json:"tz"`
		Env        map[string]string `json:"env"`
		Statusline json.RawMessage   `json:"statusline"`
		Want       map[string]any    `json:"want"`
	} `json:"gauges"`
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

func TestModParityT3(t *testing.T) {
	cases := readModParity(t).T3
	if len(cases) == 0 {
		t.Fatal("parity.json has no t3 cases")
	}
	for _, c := range cases {
		procs := map[int]t3FakeProc{}
		for k, p := range c.Procs {
			pid, err := strconv.Atoi(k)
			if err != nil {
				t.Fatalf("%s: pid %q: %v", c.Name, k, err)
			}
			procs[pid] = t3FakeProc{ppid: p.Ppid, comm: p.Comm, argv: p.Argv}
		}
		if got := fakeT3ProbeEnv(c.Env, procs, nil).spawnedByT3(context.Background()); got != c.Want {
			t.Errorf("%s: spawnedByT3() = %v, fixture says %v", c.Name, got, c.Want)
		}
	}
}

func TestModParityAgentsParse(t *testing.T) {
	cases := readModParity(t).AgentsParse
	if len(cases) == 0 {
		t.Fatal("no agents_parse cases in parity.json")
	}
	for _, c := range cases {
		rows, err := parseAgents([]byte(c.Text))
		if c.Want == nil {
			if err == nil {
				t.Errorf("%s: want an error", c.Name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		got := map[string]string{}
		for id, r := range rows {
			got[id] = r.Status
		}
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("%s: got %v, fixture says %v", c.Name, got, c.Want)
		}
	}
}

func TestModParityAgentsWait(t *testing.T) {
	cases := readModParity(t).AgentsWait
	if len(cases) == 0 {
		t.Fatal("no agents_wait cases in parity.json")
	}
	for _, c := range cases {
		var m marker
		m.State, m.Message = c.State, c.Message
		if c.Pending {
			m.PendingPermission = "fp"
		}
		got, ok := wantFor(m, agentRow{SessionID: "s", Status: c.Status})
		switch {
		case c.Want == nil && ok:
			t.Errorf("%s: Go corrects to %+v, fixture says no correction", c.Name, got)
		case c.Want != nil && (!ok || got.state != c.Want.State || got.message != c.Want.Message):
			t.Errorf("%s: Go gives %+v,%v, fixture says %+v", c.Name, got, ok, *c.Want)
		}
	}
}

func TestModParityHostName(t *testing.T) {
	for _, c := range readModParity(t).HostName {
		restore := producer.SetHostNameForTest(c.Raw)
		got := producer.ResolveSource("")
		restore()
		if got != c.Want {
			t.Errorf("source for host name %q = %q, fixture says %q", c.Raw, got, c.Want)
		}
	}
}

func statuslineGauges(in statuslineInput, env map[string]string, loc *time.Location) map[string]any {
	got := map[string]any{}
	if p, ok := extractContextPct(in); ok && contextPctEnabled(env) {
		got["context_pct"] = float64(*p)
	}
	if p, ok := extractRatePct(in); ok {
		got["rate_window_pct"] = float64(*p)
	}
	if at, ok := extractRateResetAt(in); ok {
		got["rate_reset_at"] = float64(at)
	}
	if l, ok := extractRateResetLabel(in, loc); ok {
		got["rate_reset_label"] = l
	}
	return got
}

func TestModParityGauges(t *testing.T) {
	cases := readModParity(t).Gauges
	if len(cases) == 0 {
		t.Fatal("parity.json has no gauges cases")
	}
	for _, c := range cases {
		loc, err := time.LoadLocation(c.TZ)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		in, ok := parseStatusline(c.Statusline)
		if !ok {
			t.Fatalf("%s: statusline does not parse", c.Name)
		}
		if got := statuslineGauges(in, c.Env, loc); !reflect.DeepEqual(got, c.Want) {
			t.Errorf("%s: Go maps the statusline to %v, fixture says %v", c.Name, got, c.Want)
		}
	}
}

type wireCall struct {
	Method string         `json:"method"`
	Body   map[string]any `json:"body"`
}

type modTransitions struct {
	TZ        string `json:"tz"`
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
	loc, err := time.LoadLocation(fx.TZ)
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range fx.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			var mu sync.Mutex
			var got []wireCall
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/status":
				case "/v1/usage":
					w.WriteHeader(http.StatusNoContent)
					return
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
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
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
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
				switch step.Event {
				case "tick":
					dispatchTick(context.Background(), cfg)
				case "statusline":
					var reading struct {
						SessionID  string          `json:"session_id"`
						Cwd        string          `json:"cwd"`
						Statusline json.RawMessage `json:"statusline"`
					}
					if err := json.Unmarshal(step.Input, &reading); err != nil {
						t.Fatal(err)
					}
					in, ok := parseStatusline(reading.Statusline)
					if !ok {
						t.Fatalf("step %d: statusline does not parse", i)
					}
					in.SessionID, in.Cwd = reading.SessionID, reading.Cwd
					enrichFromStatusline(in, statuslineEnv(), loc)
				default:
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
