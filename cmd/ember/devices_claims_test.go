package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	testClaimKey  = "TEST-claim-key-not-real-0000000000000000000"
	otherClaimKey = "TEST-other-key-not-real-0000000000000000000"
)

type claimFixture struct {
	app *App
	srv *httptest.Server
	clk *stepClock
}

func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()
	app, srv := newDevicesApp(t, "")
	return claimFixtureFor(app, srv)
}

func claimFixtureFor(app *App, srv *httptest.Server) *claimFixture {
	clk := &stepClock{now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	app.devices.now = clk.Now
	app.claims.now = clk.Now
	return &claimFixture{app: app, srv: srv, clk: clk}
}

func claimBody(hwID, fw, key string) string {
	b, _ := json.Marshal(map[string]string{"hw_id": hwID, "fw": fw, "claim_key": key})
	return string(b)
}

func (f *claimFixture) open(t *testing.T, key string) claimOpened {
	t.Helper()
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/claim", "", claimBody(testHwID, "0.9.110", key))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("claim = %d: %s", resp.StatusCode, b)
	}
	var c claimOpened
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *claimFixture) poll(t *testing.T, id, key string) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"claim_id": id, "claim_key": key})
	return devReq(t, f.srv, "POST", "/v1/devices/claim/poll", "", string(b))
}

func (f *claimFixture) approve(t *testing.T, id, code, name string) (*http.Response, []byte) {
	t.Helper()
	body := map[string]string{"code": code}
	if name != "" {
		body["name"] = name
	}
	b, _ := json.Marshal(body)
	return devReq(t, f.srv, "POST", "/v1/devices/claims/"+id+"/approve", testToken, string(b))
}

func (f *claimFixture) collectToken(t *testing.T, id string) claimPollReply {
	t.Helper()
	resp, b := f.poll(t, id, testClaimKey)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("poll = %d, want 200: %s", resp.StatusCode, b)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("approved poll Cache-Control = %q, want no-store", got)
	}
	var p claimPollReply
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClaimCreateValidates(t *testing.T) {
	f := newClaimFixture(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"valid", claimBody(testHwID, "0.9.110", testClaimKey), http.StatusCreated},
		{"hw_id with colons", claimBody("3C:DC:75:61:FC:8D", "", otherClaimKey), http.StatusCreated},
		{"short hw_id", claimBody("3cdc7561fc", "0.9.110", testClaimKey), http.StatusBadRequest},
		{"non-hex hw_id", claimBody("3cdc7561fczz", "0.9.110", testClaimKey), http.StatusBadRequest},
		{"missing key", claimBody(testHwID, "0.9.110", ""), http.StatusBadRequest},
		{"short key", claimBody(testHwID, "0.9.110", testClaimKey[:42]), http.StatusBadRequest},
		{"long key", claimBody(testHwID, "0.9.110", testClaimKey+"A"), http.StatusBadRequest},
		{"padded key", claimBody(testHwID, "0.9.110", testClaimKey[:42]+"="), http.StatusBadRequest},
		{"standard base64 key", claimBody(testHwID, "0.9.110", "TEST+claim/key-not-real-0000000000000000000"), http.StatusBadRequest},
		{"non-canonical key", claimBody(testHwID, "0.9.110", testClaimKey[:42]+"1"), http.StatusBadRequest},
		{"fw too long", claimBody(testHwID, strings.Repeat("9", 33), testClaimKey), http.StatusBadRequest},
		{"not JSON", `{"hw_id":`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.app.claims.claims = map[string]*deviceClaim{}
			resp, b := devReq(t, f.srv, "POST", "/v1/devices/claim", "", c.body)
			if resp.StatusCode != c.want {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, c.want, b)
			}
		})
	}
}

type scriptedRand struct{ b []byte }

func (s *scriptedRand) Read(p []byte) (int, error) {
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

func TestClaimCodeIsSixDigitsAndUnique(t *testing.T) {
	f := newClaimFixture(t)
	c := f.open(t, testClaimKey)
	if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(c.Code) {
		t.Errorf("code = %q, want 6 digits", c.Code)
	}
	if !regexp.MustCompile(`^c_[0-9a-f]{8}$`).MatchString(c.ClaimID) {
		t.Errorf("claim_id = %q, want c_ + 8 hex", c.ClaimID)
	}
	if c.ExpiresInS != 600 || c.PollS != 5 {
		t.Errorf("expires_in_s, poll_s = %d, %d, want 600, 5", c.ExpiresInS, c.PollS)
	}

	reg := newClaimRegistry()
	reg.rand = &scriptedRand{b: []byte{
		0x01, 0x23, 0xab, 0xcd, 0x00, 0x07, 0x5e, 0x61,
		0x01, 0x23, 0xab, 0xcd, 0x01, 0x23, 0xab, 0xce,
		0xff, 0xff, 0xff, 0xff, 0x00, 0x07, 0x5e, 0x61, 0x00, 0x00, 0x00, 0x07,
	}}
	first, err := reg.open(testHwID, "", "192.0.2.1", testClaimKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reg.open(testHwID, "", "192.0.2.2", testClaimKey)
	if err != nil {
		t.Fatal(err)
	}
	if first.id != "c_0123abcd" || first.code != "482913" {
		t.Fatalf("first = %s %s, want c_0123abcd 482913", first.id, first.code)
	}
	if second.id != "c_0123abce" {
		t.Errorf("second id = %s, want the next draw after the taken c_0123abcd", second.id)
	}
	if second.code != "000007" {
		t.Errorf("second code = %s, want 000007: a draw past the unbiased range and a pending code are both skipped", second.code)
	}
}

func TestClaimCapsPerIPAndGlobalWithoutEviction(t *testing.T) {
	f := newClaimFixture(t)
	a := f.open(t, testClaimKey)
	b := f.open(t, otherClaimKey)
	resp, body := devReq(t, f.srv, "POST", "/v1/devices/claim", "", claimBody(testHwID, "", testClaimKey))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third claim from one IP = %d, want 429: %s", resp.StatusCode, body)
	}
	for i := range claimMaxTotal - 2 {
		if _, err := f.app.claims.open(testHwID, "", fmt.Sprintf("192.0.2.%d", i+1), testClaimKey); err != nil {
			t.Fatalf("claim %d from its own IP: %v", i+3, err)
		}
	}
	if _, err := f.app.claims.open(testHwID, "", "198.51.100.7", testClaimKey); !errors.Is(err, errClaimsFull) {
		t.Fatalf("claim past the global cap: err = %v, want errClaimsFull", err)
	}
	for _, c := range []struct{ id, key string }{{a.ClaimID, testClaimKey}, {b.ClaimID, otherClaimKey}} {
		if resp, body := f.poll(t, c.id, c.key); resp.StatusCode != http.StatusAccepted {
			t.Errorf("first claims after the flood: poll %s = %d, want 202: %s", c.id, resp.StatusCode, body)
		}
	}
}

func TestClaimExpiresAfterTenMinutes(t *testing.T) {
	f := newClaimFixture(t)
	f.open(t, otherClaimKey)
	c := f.open(t, testClaimKey)
	f.clk.advance(claimTTL - time.Second)
	resp, b := f.poll(t, c.ClaimID, testClaimKey)
	if resp.StatusCode != http.StatusAccepted || !bytes.Contains(b, []byte(`"expires_in_s":1`)) {
		t.Fatalf("poll a second before expiry = %d %s, want 202 with expires_in_s 1", resp.StatusCode, b)
	}
	f.clk.advance(time.Second)
	if resp, b := f.poll(t, c.ClaimID, testClaimKey); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("poll at expiry = %d, want 404: %s", resp.StatusCode, b)
	}
	if resp, b := f.approve(t, c.ClaimID, c.Code, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("approve an expired claim = %d, want 404: %s", resp.StatusCode, b)
	}
	f.open(t, testClaimKey)
	f.open(t, otherClaimKey)
}

func TestClaimPollNeedsTheKey(t *testing.T) {
	f := newClaimFixture(t)
	c := f.open(t, testClaimKey)
	expired := f.open(t, otherClaimKey)
	wrongResp, wrong := f.poll(t, c.ClaimID, otherClaimKey)
	unknownResp, unknown := f.poll(t, "c_00000000", testClaimKey)
	noKeyResp, noKey := f.poll(t, c.ClaimID, "")
	f.clk.advance(claimTTL)
	expiredResp, gone := f.poll(t, expired.ClaimID, otherClaimKey)
	for name, r := range map[string]*http.Response{"wrong key": wrongResp, "unknown id": unknownResp, "no key": noKeyResp, "expired": expiredResp} {
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, r.StatusCode)
		}
	}
	if !bytes.Equal(wrong, gone) || !bytes.Equal(unknown, gone) || !bytes.Equal(noKey, gone) {
		t.Errorf("404 bodies differ: wrong key %s, unknown %s, no key %s, expired %s", wrong, unknown, noKey, gone)
	}
}

func TestClaimApproveWrongCodeCountsAndDropsAtFive(t *testing.T) {
	f := newClaimFixture(t)
	c := f.open(t, testClaimKey)
	wrong := "000000"
	if c.Code == wrong {
		wrong = "111111"
	}
	for want := claimMaxAttempts - 1; want >= 0; want-- {
		resp, b := f.approve(t, c.ClaimID, wrong, "")
		var e struct {
			Error        string `json:"error"`
			AttemptsLeft *int   `json:"attempts_left"`
		}
		_ = json.Unmarshal(b, &e)
		if resp.StatusCode != http.StatusBadRequest || e.Error != "wrong_code" || e.AttemptsLeft == nil || *e.AttemptsLeft != want {
			t.Fatalf("wrong code: %d %s, want 400 wrong_code attempts_left %d", resp.StatusCode, b, want)
		}
	}
	if resp, b := f.approve(t, c.ClaimID, c.Code, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("right code after five wrong ones = %d, want 404: %s", resp.StatusCode, b)
	}
	if resp, _ := f.poll(t, c.ClaimID, testClaimKey); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("poll a dropped claim = %d, want 404", resp.StatusCode)
	}
	if len(f.app.devices.list()) != 0 {
		t.Fatal("a dropped claim minted a device")
	}
}

func TestClaimApproveMintsAndPollDeliversSameTokenUntilFirstUse(t *testing.T) {
	f := newClaimFixture(t)
	epoch := f.app.devices.epochValue()
	c := f.open(t, testClaimKey)
	code := c.Code[:3] + " " + c.Code[3:]
	resp, b := f.approve(t, c.ClaimID, code, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d: %s", resp.StatusCode, b)
	}
	if bytes.Contains(b, []byte("ekd_")) || bytes.Contains(b, []byte("token")) {
		t.Fatalf("approve reply carries a token: %s", b)
	}
	var view deviceView
	if err := json.Unmarshal(b, &view); err != nil || view.ID != "knob-61fc8c" || view.Name != "Knob 61FC8C" || view.HwID != testHwID {
		t.Fatalf("approve view = %s", b)
	}
	if f.app.devices.epochValue() == epoch {
		t.Error("approve did not bump the epoch")
	}
	if resp, b := f.approve(t, c.ClaimID, c.Code, ""); resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte(`"id":"knob-61fc8c"`)) {
		t.Fatalf("second approve = %d %s, want 200 with the same device", resp.StatusCode, b)
	}
	resp, list := devReq(t, f.srv, "GET", "/v1/devices/claims", testToken, "")
	if resp.StatusCode != http.StatusOK || !bytes.Equal(bytes.TrimSpace(list), []byte(`{"claims":[]}`)) {
		t.Errorf("claims after approval = %d %s, want the approved claim left out", resp.StatusCode, list)
	}

	first := f.collectToken(t, c.ClaimID)
	if first.State != "approved" || first.DeviceID != "knob-61fc8c" || first.Name != "Knob 61FC8C" || !strings.HasPrefix(first.Token, "ekd_") {
		t.Fatalf("approved poll = %+v", first)
	}
	f.clk.advance(claimTTL - time.Second)
	if again := f.collectToken(t, c.ClaimID); again.Token != first.Token {
		t.Fatal("a second poll before first use delivered another token")
	}
	if resp, out := checkin(t, f.srv, first.Token, 1); resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin with the delivered token = %d: %v", resp.StatusCode, out)
	}
	if resp, b := f.poll(t, c.ClaimID, testClaimKey); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("poll after the token's first use = %d, want 404: %s", resp.StatusCode, b)
	}
}

func TestClaimApprovalOutlivesTheCodeForTenMinutes(t *testing.T) {
	f := newClaimFixture(t)
	c := f.open(t, testClaimKey)
	f.clk.advance(claimTTL - time.Minute)
	if resp, b := f.approve(t, c.ClaimID, c.Code, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d: %s", resp.StatusCode, b)
	}
	f.clk.advance(claimTTL - time.Second)
	f.collectToken(t, c.ClaimID)
	f.clk.advance(time.Second)
	if resp, _ := f.poll(t, c.ClaimID, testClaimKey); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("poll ten minutes after approval = %d, want 404", resp.StatusCode)
	}
}

func TestClaimApproveReprovisionKeepsConfigAndRevokesOldToken(t *testing.T) {
	f := newClaimFixture(t)
	m := mintKnob(t, f.srv, http.StatusCreated)
	putKnobConfig(t, f.srv, m.ID, `{"poll_ms":4000}`)
	_, version, err := f.app.devices.config(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := f.open(t, testClaimKey)
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/claims", testToken, "")
	if resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte(`"known_device_id":"knob-61fc8c"`)) {
		t.Fatalf("claims = %d %s, want known_device_id knob-61fc8c", resp.StatusCode, b)
	}
	if resp, b := f.approve(t, c.ClaimID, c.Code, "Shelf knob"); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d: %s", resp.StatusCode, b)
	}
	got := f.collectToken(t, c.ClaimID)
	if got.DeviceID != m.ID || got.Name != "Shelf knob" || got.Token == m.Token {
		t.Fatalf("re-provision poll = %+v", got)
	}
	if resp, _ := checkin(t, f.srv, m.Token, version); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after re-pair = %d, want 401", resp.StatusCode)
	}
	cfg, after, err := f.app.devices.config(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != version || cfg.PollMS != 4000 {
		t.Fatalf("config after re-pair: version %d poll_ms %d, want %d and 4000", after, cfg.PollMS, version)
	}
	if resp, out := checkin(t, f.srv, got.Token, version); resp.StatusCode != http.StatusOK || out["config"] != nil {
		t.Fatalf("checkin with the new token = %d %v, want 200 without a config push", resp.StatusCode, out)
	}
	if n := len(f.app.devices.list()); n != 1 {
		t.Fatalf("records after re-pair = %d, want 1", n)
	}
}

func TestClaimRefusedWhileRecordIsLive(t *testing.T) {
	f := newClaimFixture(t)
	m := mintKnob(t, f.srv, http.StatusCreated)
	if resp, _ := checkin(t, f.srv, m.Token, 1); resp.StatusCode != http.StatusOK {
		t.Fatal("checkin failed")
	}
	f.clk.advance(claimLiveWindow - time.Second)
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/claim", "", claimBody(testHwID, "", testClaimKey))
	if resp.StatusCode != http.StatusConflict || !bytes.Contains(b, []byte(`"error":"already_paired"`)) {
		t.Fatalf("claim for a live knob = %d %s, want 409 already_paired", resp.StatusCode, b)
	}
	f.clk.advance(time.Second)
	f.open(t, testClaimKey)
}

func TestClaimsAreForgottenOnRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app, srv := newDevicesApp(t, db)
	f := claimFixtureFor(app, srv)
	c := f.open(t, testClaimKey)
	if resp, b := f.approve(t, c.ClaimID, c.Code, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d: %s", resp.StatusCode, b)
	}
	srv.Close()
	_ = app.store.Close()

	app2, srv2 := newDevicesApp(t, db)
	f2 := claimFixtureFor(app2, srv2)
	if resp, b := f2.poll(t, c.ClaimID, testClaimKey); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("poll after restart = %d, want 404: %s", resp.StatusCode, b)
	}
	if len(app2.devices.list()) != 1 {
		t.Fatal("the approved record did not survive the restart")
	}
	f2.open(t, testClaimKey)
}

func TestClaimDeclineDropsTheClaim(t *testing.T) {
	f := newClaimFixture(t)
	c := f.open(t, testClaimKey)
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/claims/"+c.ClaimID+"/decline", testToken, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("decline = %d: %s", resp.StatusCode, b)
	}
	if resp, _ := f.poll(t, c.ClaimID, testClaimKey); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("poll a declined claim = %d, want 404", resp.StatusCode)
	}
	if resp, _ := devReq(t, f.srv, "POST", "/v1/devices/claims/"+c.ClaimID+"/decline", testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("decline twice = %d, want 404", resp.StatusCode)
	}
}

func TestClaimListShowsPendingClaimsWithoutSecrets(t *testing.T) {
	f := newClaimFixture(t)
	c := f.open(t, testClaimKey)
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/claims", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d: %s", resp.StatusCode, b)
	}
	keyHash := sha256.Sum256([]byte(testClaimKey))
	for _, secret := range []string{c.Code, testClaimKey, hex.EncodeToString(keyHash[:])} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatalf("list leaks %q: %s", secret, b)
		}
	}
	want := `{"claims":[{"id":"` + c.ClaimID + `","hw_id":"3cdc7561fc8c","short_id":"61FC8C","fw":"0.9.110","ip":"127.0.0.1",` +
		`"created_at":"2026-10-21T12:00:00Z","expires_at":"2026-10-21T12:10:00Z","known_device_id":null}]}`
	if got := string(bytes.TrimSpace(b)); got != want {
		t.Fatalf("list\n got %s\nwant %s", got, want)
	}
}

func TestClaimRoutesAreRateLimited(t *testing.T) {
	f := newClaimFixture(t)
	f.app.updateConfig(func(c *Config) {
		c.RateLimit = RateLimitConfig{Burst: 2, RefillPerSec: 0.001, IdleEvictSeconds: 300}
	})
	c := f.open(t, testClaimKey)
	if resp, _ := f.poll(t, c.ClaimID, testClaimKey); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("poll within the burst = %d, want 202", resp.StatusCode)
	}
	resp, _ := f.poll(t, c.ClaimID, testClaimKey)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("poll past the burst = %d, want 429 with Retry-After", resp.StatusCode)
	}
	if resp, _ := devReq(t, f.srv, "POST", "/v1/devices/claim", "", claimBody(testHwID, "", otherClaimKey)); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("claim past the burst = %d, want 429", resp.StatusCode)
	}
}

func TestClaimSecretsNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Auth.StatusToken = testToken
	cfg.RateLimit.Disabled = true
	app := NewApp(cfg, &recordingPublisher{}, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err := app.ensureStore(filepath.Join(t.TempDir(), "s.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.store.Close() })
	if err := app.loadRegistries(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	ownIconJobs(t, app)
	f := claimFixtureFor(app, srv)

	declined := f.open(t, otherClaimKey)
	devReq(t, srv, "POST", "/v1/devices/claims/"+declined.ClaimID+"/decline", testToken, "")
	expired := f.open(t, otherClaimKey)
	f.clk.advance(claimTTL)
	f.poll(t, expired.ClaimID, otherClaimKey)

	c := f.open(t, testClaimKey)
	f.poll(t, c.ClaimID, testClaimKey)
	f.poll(t, c.ClaimID, otherClaimKey)
	devReq(t, srv, "POST", "/v1/devices/claim", "", claimBody("nothex", "", testClaimKey))
	devReq(t, srv, "POST", "/v1/devices/claim", "", claimBody(testHwID, "", testClaimKey[:20]))
	wrong := "000000"
	if c.Code == wrong {
		wrong = "111111"
	}
	f.approve(t, c.ClaimID, wrong, "")
	if resp, b := f.approve(t, c.ClaimID, c.Code, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d: %s", resp.StatusCode, b)
	}
	got := f.collectToken(t, c.ClaimID)
	if resp, _ := checkin(t, srv, got.Token, 1); resp.StatusCode != http.StatusOK {
		t.Fatal("checkin with the delivered token failed")
	}

	out := logs.String()
	keyHash := sha256.Sum256([]byte(testClaimKey))
	otherHash := sha256.Sum256([]byte(otherClaimKey))
	secrets := map[string]string{
		"code": c.Code, "declined code": declined.Code, "expired code": expired.Code,
		"claim key": testClaimKey, "other claim key": otherClaimKey, "claim key prefix": testClaimKey[:20],
		"claim key hash": hex.EncodeToString(keyHash[:]), "other key hash": hex.EncodeToString(otherHash[:]),
		"token": got.Token, "token hash": tokenHash(got.Token),
	}
	for name, s := range secrets {
		if strings.Contains(out, s) {
			t.Errorf("logs contain the %s %q:\n%s", name, s, out)
		}
	}
	for _, msg := range []string{"device claim created", "device claim declined", "device claim expired",
		"device claim code rejected", "device claim approved", "device claim collected"} {
		if !strings.Contains(out, msg) {
			t.Errorf("logs lack %q:\n%s", msg, out)
		}
	}
}

func TestClaimRoutesInOpenAPI(t *testing.T) {
	want := map[string]string{
		"POST /v1/devices/claim":               "public",
		"POST /v1/devices/claim/poll":          "public",
		"GET /v1/devices/claims":               "master",
		"POST /v1/devices/claims/{id}/approve": "master",
		"POST /v1/devices/claims/{id}/decline": "master",
	}
	got := map[string]string{}
	for _, op := range readSpecOps(t) {
		if _, ok := want[op.route]; !ok {
			continue
		}
		switch {
		case op.auth.public && !op.auth.owner && !op.auth.device:
			got[op.route] = "public"
		case op.auth.owner && op.auth.masterOnly && !op.auth.device:
			got[op.route] = "master"
		default:
			got[op.route] = fmt.Sprintf("%+v", op.auth)
		}
	}
	for route, w := range want {
		if got[route] != w {
			t.Errorf("%s: spec auth = %q, want %s", route, got[route], w)
		}
	}
	routes := registeredRoutes(t)
	for route := range want {
		if !slices.Contains(routes, route) {
			t.Errorf("%s is not registered", route)
		}
	}
}

func TestDeviceIDsCanNeverBeClaimSegments(t *testing.T) {
	st := &deviceState{}
	for _, hw := range []string{testHwID, "000000000000", "ffffffffffff"} {
		for range 2 {
			id := deviceID(st, hw)
			st.Devices = append(st.Devices, deviceRecord{ID: id, Kind: deviceKindKnob, HwID: hw})
			if !strings.HasPrefix(id, "knob-") {
				t.Errorf("knob id %q lacks the knob- prefix", id)
			}
		}
	}
	for _, prefix := range []string{clientIDPrefix, clockIDPrefix} {
		if prefix == "" || strings.HasPrefix("claim", prefix) || strings.HasPrefix("claims", prefix) {
			t.Errorf("id prefix %q could produce a claim segment", prefix)
		}
	}
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	client := mintClient(t, srv, "ci", scopeIngest)
	for _, id := range []string{m.ID, client.ID} {
		if id == "claim" || id == "claims" {
			t.Fatalf("minted id %q shadows a claim route", id)
		}
	}
	resp, b := devReq(t, srv, "GET", "/v1/devices/claims", testToken, "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(b), `{"claims":`) {
		t.Fatalf("claims list = %d %s", resp.StatusCode, b)
	}
}
