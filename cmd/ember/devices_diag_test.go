package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const fullDiag = `{"boots":42,"crash":{"pc":"0x4201a2b3","reason":"panic","task":"ember"},"heap_internal_min":71234,"heap_largest_min":30720,"reset_reason":"poweron","stack_free":{"ember":1880,"eye":900,"lvgl":2304}}`

func checkinDiag(t *testing.T, srvURL func(string) (*http.Response, []byte), diag string) {
	t.Helper()
	body := `{"fw":"0.9.13","rssi":-70}`
	if diag != "" {
		body = `{"fw":"0.9.13","rssi":-70,"diag":` + diag + `}`
	}
	if resp, b := srvURL(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
	}
}

func newDiagKnob(t *testing.T) (*App, func(string)) {
	t.Helper()
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	return app, func(diag string) {
		t.Helper()
		checkinDiag(t, func(body string) (*http.Response, []byte) {
			return devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, body)
		}, diag)
	}
}

func storedDiag(app *App) *deviceDiag {
	return app.devices.list()[0].LastCheckin.Diag
}

func TestDeviceCheckinRecordsTheDiag(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	if resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"fw":"0.9.13","diag":`+fullDiag+`}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
	}
	want := deviceDiag{
		Boots:           42,
		Crash:           &deviceCrash{PC: "0x4201a2b3", Reason: "panic", Task: "ember"},
		HeapInternalMin: 71234,
		HeapLargestMin:  30720,
		ResetReason:     "poweron",
		StackFree:       map[string]int{"ember": 1880, "eye": 900, "lvgl": 2304},
	}
	got := storedDiag(app)
	if got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("diag = %+v, want %+v", got, want)
	}
	got.StackFree["ember"] = 1
	got.Crash.Task = "x"
	if again := storedDiag(app); again.StackFree["ember"] != 1880 || again.Crash.Task != "ember" {
		t.Fatalf("list shares the stored diag: %+v", again)
	}
	resp, b := devReq(t, srv, "GET", "/v1/devices", testToken, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"diag":`+fullDiag) {
		t.Fatalf("GET /v1/devices = %d: %s", resp.StatusCode, b)
	}
}

func TestDeviceCheckinKeepsAnEmptyDiag(t *testing.T) {
	app, post := newDiagKnob(t)
	post(`{}`)
	if got := storedDiag(app); got == nil || !reflect.DeepEqual(*got, deviceDiag{}) {
		t.Fatalf("diag = %+v, want zero object", got)
	}
}

func TestDeviceCheckinDropsAnInvalidDiagButKeepsTheCheckin(t *testing.T) {
	for name, diag := range map[string]string{
		"boots":              `{"boots":-1}`,
		"heap_internal_min":  `{"heap_internal_min":-1}`,
		"heap_largest_min":   `{"heap_largest_min":-1}`,
		"reset_reason case":  `{"reset_reason":"PowerOn"}`,
		"reset_reason long":  `{"reset_reason":"` + strings.Repeat("a", 25) + `"}`,
		"crash reason":       `{"crash":{"reason":"bad reason","pc":"0x1","task":"t"}}`,
		"crash pc":           `{"crash":{"reason":"panic","pc":"4201a2b3","task":"t"}}`,
		"crash pc long":      `{"crash":{"reason":"panic","pc":"0x4201a2b3f","task":"t"}}`,
		"crash task long":    `{"crash":{"reason":"panic","pc":"0x1","task":"` + strings.Repeat("t", 17) + `"}}`,
		"crash task control": `{"crash":{"reason":"panic","pc":"0x1","task":"a\u0001"}}`,
		"stack task empty":   `{"stack_free":{"":1}}`,
		"stack task long":    `{"stack_free":{"` + strings.Repeat("t", 17) + `":1}}`,
		"stack task utf8":    `{"stack_free":{"задача":1}}`,
		"stack negative":     `{"stack_free":{"ember":-1}}`,
		"stack too many":     manyStacks(33),
		"wrong type":         `{"boots":"42"}`,
		"not object":         `[1]`,
	} {
		t.Run(name, func(t *testing.T) {
			app, post := newDiagKnob(t)
			post(diag)
			got := app.devices.list()[0].LastCheckin
			if got.Diag != nil || got.FW != "0.9.13" || got.RSSI != -70 {
				t.Fatalf("checkin = %+v, diag %+v", got, got.Diag)
			}
		})
	}
}

func manyStacks(n int) string {
	var b strings.Builder
	b.WriteString(`{"stack_free":{`)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"t` + strings.Repeat("x", i%10) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":1`)
	}
	b.WriteString(`}}`)
	return b.String()
}

func TestDeviceCheckinAcceptsTheMostStacks(t *testing.T) {
	app, post := newDiagKnob(t)
	post(manyStacks(32))
	if got := storedDiag(app); got == nil || len(got.StackFree) != 32 {
		t.Fatalf("diag = %+v", got)
	}
}

func TestDeviceCheckinIgnoresServerDerivedDiagKeysFromTheKnob(t *testing.T) {
	app, post := newDiagKnob(t)
	post(`{"boots":3,"reboots":99,"prev_reset_reason":"panic","reboots_since_seen":7}`)
	if got := storedDiag(app); got == nil || got.Reboots != 0 || got.PrevResetReason != "" || got.RebootsSinceSeen != 0 {
		t.Fatalf("diag = %+v", got)
	}
}

func TestDeviceCheckinWithoutDiagClearsIt(t *testing.T) {
	app, post := newDiagKnob(t)
	post(fullDiag)
	post("")
	if got := storedDiag(app); got != nil {
		t.Fatalf("older firmware: diag = %+v", got)
	}
}

func TestDeviceCheckinCountsRebootsFromBoots(t *testing.T) {
	app, post := newDiagKnob(t)
	post(`{"boots":40,"reset_reason":"poweron"}`)
	if got := storedDiag(app); got.Reboots != 0 || got.PrevResetReason != "" {
		t.Fatalf("first diag = %+v", got)
	}
	post(`{"boots":40,"reset_reason":"poweron"}`)
	if got := storedDiag(app); got.Reboots != 0 {
		t.Fatalf("same boot = %+v", got)
	}
	post(`{"boots":41,"reset_reason":"panic"}`)
	if got := storedDiag(app); got.Reboots != 1 || got.PrevResetReason != "poweron" {
		t.Fatalf("after one reboot = %+v", got)
	}
	post(`{"boots":41,"reset_reason":"panic"}`)
	if got := storedDiag(app); got.Reboots != 1 || got.PrevResetReason != "poweron" {
		t.Fatalf("same boot keeps the count = %+v", got)
	}
	post(`{"boots":44,"reset_reason":"task_wdt"}`)
	if got := storedDiag(app); got.Reboots != 4 || got.PrevResetReason != "" || got.RebootsSinceSeen != 3 {
		t.Fatalf("after three reboots between checkins = %+v", got)
	}
	post(`{"boots":44,"reset_reason":"task_wdt"}`)
	if got := storedDiag(app); got.Reboots != 4 || got.PrevResetReason != "" || got.RebootsSinceSeen != 3 {
		t.Fatalf("same boot keeps the gap = %+v", got)
	}
	post(`{"boots":45,"reset_reason":"sw"}`)
	if got := storedDiag(app); got.Reboots != 5 || got.PrevResetReason != "task_wdt" || got.RebootsSinceSeen != 0 {
		t.Fatalf("one reboot after a gap = %+v", got)
	}
	post(`{"boots":1,"reset_reason":"sw"}`)
	if got := storedDiag(app); got.Reboots != 6 || got.PrevResetReason != "sw" || got.RebootsSinceSeen != 0 {
		t.Fatalf("boots counter reset = %+v", got)
	}
	post(`{"reset_reason":"sw"}`)
	if got := storedDiag(app); got.Reboots != 6 || got.PrevResetReason != "sw" {
		t.Fatalf("unknown boots = %+v", got)
	}
}

func TestDeviceCheckinLogsACrashOnceUntilItChanges(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	var logs bytes.Buffer
	app.logger = slog.New(slog.NewTextHandler(&logs, nil))
	m := mintKnob(t, srv, http.StatusCreated)
	post := func(diag string) {
		t.Helper()
		checkinDiag(t, func(body string) (*http.Response, []byte) {
			return devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, body)
		}, diag)
	}
	crashes := func() int { return strings.Count(logs.String(), "level=WARN msg=\"knob crash reported\"") }
	post(`{"boots":5}`)
	if got := crashes(); got != 0 {
		t.Fatalf("crash logs without a crash = %d", got)
	}
	for range 3 {
		post(fullDiag)
	}
	if got := crashes(); got != 1 {
		t.Fatalf("crash logs for one repeated crash = %d, want 1:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), "pc=0x4201a2b3") || !strings.Contains(logs.String(), "task=ember") || !strings.Contains(logs.String(), "reason=panic") {
		t.Fatalf("crash log lacks its details:\n%s", logs.String())
	}
	post(`{"crash":{"pc":"0x4201a2b3","reason":"panic","task":"lvgl"}}`)
	post(`{"crash":{"pc":"0x4201a2b3","reason":"task_wdt","task":"lvgl"}}`)
	post(`{"crash":{"pc":"0x42000000","reason":"task_wdt","task":"lvgl"}}`)
	if got := crashes(); got != 4 {
		t.Fatalf("crash logs after task, reason and pc changed = %d, want 4", got)
	}
	post(`{"boots":6}`)
	post(`{"crash":{"pc":"0x42000000","reason":"task_wdt","task":"lvgl"}}`)
	if got := crashes(); got != 5 {
		t.Fatalf("crash logs after the dump was cleared and came back = %d, want 5", got)
	}
}

func TestDeviceCheckinLogsADroppedDiagOnlyWhenTheReasonChanges(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	var logs bytes.Buffer
	app.logger = slog.New(slog.NewTextHandler(&logs, nil))
	m := mintKnob(t, srv, http.StatusCreated)
	post := func(diag string) {
		t.Helper()
		checkinDiag(t, func(body string) (*http.Response, []byte) {
			return devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, body)
		}, diag)
	}
	dropped := func() int { return strings.Count(logs.String(), "level=INFO msg=\"device diag dropped\"") }
	for range 3 {
		post(`{"boots":-1}`)
	}
	if got := dropped(); got != 1 {
		t.Fatalf("Info logs for one repeated reason = %d, want 1:\n%s", got, logs.String())
	}
	post(`{"heap_internal_min":-1}`)
	if got := dropped(); got != 2 {
		t.Fatalf("Info logs after the reason changed = %d, want 2", got)
	}
}

func TestDeviceLastCheckinDiagSurvivesRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app1, srv1 := newDevicesApp(t, db)
	m := mintKnob(t, srv1, http.StatusCreated)
	devReq(t, srv1, "POST", "/v1/devices/self/checkin", m.Token, `{"fw":"0.9.13","diag":{"boots":41,"reset_reason":"poweron"}}`)
	devReq(t, srv1, "POST", "/v1/devices/self/checkin", m.Token, `{"fw":"0.9.13","diag":`+fullDiag+`}`)
	srv1.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app1.shutdown(ctx, &http.Server{}, &sync.WaitGroup{})

	app2, _ := newDevicesApp(t, db)
	got := storedDiag(app2)
	if got == nil || got.Boots != 42 || got.Reboots != 1 || got.PrevResetReason != "poweron" || got.Crash == nil || got.StackFree["lvgl"] != 2304 {
		t.Fatalf("diag after restart = %+v", got)
	}
}
