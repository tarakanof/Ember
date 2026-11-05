package printer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testPassword = "pw-sentinel-7f3a9c"
	testRealm    = "Printer API"
)

type stubPrinter struct {
	srv *httptest.Server

	mu         sync.Mutex
	password   string
	nonceSeq   int
	valid      map[string]bool
	status     string
	job        string
	thumb      []byte
	thumbType  string
	thumbCode  int
	version    string
	hits       map[string]int
	unauth     int
	staleSent  int
	authorized []string
}

func newStubPrinter(t *testing.T) *stubPrinter {
	t.Helper()
	s := &stubPrinter{
		password:  testPassword,
		valid:     map[string]bool{},
		hits:      map[string]int{},
		status:    statusJSON("IDLE", nil),
		version:   `{"api":"2.0.0","server":"2.1.2","text":"PrusaLink","hostname":"prusa-core-one","firmware":"8.0.0+12345"}`,
		thumbType: "image/png",
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func md5Of(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (s *stubPrinter) newNonceLocked() string {
	s.nonceSeq++
	n := fmt.Sprintf("%016x", 0xabc0000+s.nonceSeq)
	s.valid[n] = true
	return n
}

func (s *stubPrinter) challengeLocked(w http.ResponseWriter, stale bool) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm="%s", nonce="%s", stale=%t`, testRealm, s.newNonceLocked(), stale))
	if stale {
		s.staleSent++
	}
	s.unauth++
	w.WriteHeader(http.StatusUnauthorized)
}

func authParams(h string) map[string]string {
	out := map[string]string{}
	rest, ok := strings.CutPrefix(h, "Digest ")
	if !ok {
		return out
	}
	for _, part := range strings.Split(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			out[k] = strings.Trim(v, `"`)
		}
	}
	return out
}

func (s *stubPrinter) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := authParams(r.Header.Get("Authorization"))
	if len(p) == 0 {
		s.challengeLocked(w, false)
		return
	}
	ha1 := md5Of(p["username"] + ":" + testRealm + ":" + s.password)
	want := md5Of(ha1 + ":" + p["nonce"] + ":" + md5Of(r.Method+":"+r.URL.RequestURI()))
	if p["username"] != "maker" || p["uri"] != r.URL.RequestURI() || p["response"] != want || s.password == "" {
		s.challengeLocked(w, false)
		return
	}
	if !s.valid[p["nonce"]] {
		s.challengeLocked(w, true)
		return
	}
	s.hits[r.URL.Path]++
	s.authorized = append(s.authorized, r.URL.Path)
	switch {
	case r.URL.Path == "/api/v1/status":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.status))
	case r.URL.Path == "/api/v1/job":
		if s.job == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.job))
	case r.URL.Path == "/api/version":
		if s.version == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.version))
	case strings.HasPrefix(r.URL.Path, "/thumb/l/"):
		if s.thumbCode != 0 {
			w.WriteHeader(s.thumbCode)
			return
		}
		if s.thumb == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", s.thumbType)
		_, _ = w.Write(s.thumb)
	default:
		http.NotFound(w, r)
	}
}

func (s *stubPrinter) set(f func(s *stubPrinter)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func (s *stubPrinter) expireNonces() {
	s.set(func(s *stubPrinter) { clear(s.valid) })
}

func (s *stubPrinter) hit(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func (s *stubPrinter) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.authorized) + s.unauth
}

type jobFields struct {
	id                    int
	progress              float64
	remaining, printing   *float64
	nozzle, nozzleT       float64
	bed, bedT             float64
	filamentChangeSeconds *float64
}

func num(v float64) *float64 { return &v }

func statusJSON(state string, j *jobFields) string {
	temps := jobFields{nozzle: 25, bed: 24}
	if j != nil {
		temps = *j
	}
	printer := fmt.Sprintf(`"printer":{"state":%q,"temp_bed":%.1f,"target_bed":%.1f,"temp_nozzle":%.1f,"target_nozzle":%.1f,"axis_z":18.4,"flow":100,"speed":100}`,
		state, temps.bed, temps.bedT, temps.nozzle, temps.nozzleT)
	if j == nil {
		return "{" + printer + "}"
	}
	job := fmt.Sprintf(`"id":%d,"progress":%.2f`, j.id, j.progress)
	if j.remaining != nil {
		job += fmt.Sprintf(`,"time_remaining":%d`, int(*j.remaining))
	}
	if j.printing != nil {
		job += fmt.Sprintf(`,"time_printing":%d`, int(*j.printing))
	}
	if j.filamentChangeSeconds != nil {
		job += fmt.Sprintf(`,"filament_change_in":%d`, int(*j.filamentChangeSeconds))
	}
	return `{"job":{` + job + `},"storage":{"path":"/usb/","name":"usb","read_only":false},` + printer + "}"
}

func jobJSON(id int, state, display string) string {
	return fmt.Sprintf(`{"id":%d,"state":%q,"progress":42.00,"time_remaining":4680,"time_printing":3360,"file":{"refs":{"icon":"/thumb/s/usb/BRACKE~1.BGC","thumbnail":"/thumb/l/usb/BRACKE~1.BGC","download":"/usb/BRACKE~1.BGC"},"name":"BRACKE~1.BGC","display_name":%q,"path":"/usb","size":2393142,"m_timestamp":1730800000}}`,
		id, state, display)
}

type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logSink) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(l.buf.String()), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type harness struct {
	t    *testing.T
	p    *Poller
	cfg  Config
	now  time.Time
	logs *logSink
	mu   sync.Mutex
}

func newHarness(t *testing.T, cfg Config, password string, browse BrowseFunc) *harness {
	t.Helper()
	h := &harness{t: t, cfg: cfg, now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC), logs: &logSink{}}
	h.p = NewPoller(Options{
		Config: func() Config { h.mu.Lock(); defer h.mu.Unlock(); return h.cfg },
		Browse: browse,
		Logger: slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:    func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
	})
	h.p.SetCredentials(Credentials{User: DefaultUser, Password: password})
	return h
}

func manualConfig(url string) Config {
	c := Config{Enabled: true, URL: url}
	c.ApplyDefaults()
	return c
}

func (h *harness) step() time.Duration {
	h.t.Helper()
	d := h.p.Step(context.Background())
	return d
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}

func (h *harness) snap() Snapshot { return h.p.State.At(h.p.now()) }
