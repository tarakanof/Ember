package main

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func liveLikeRotation() []map[string]any {
	return []map[string]any{
		{"name": "ember", "enabled": true, "inLoop": true, "slot": 0, "present": true, "origin": "pushed"},
		{"name": "ember-weather", "enabled": true, "inLoop": true, "slot": 1, "present": true, "origin": "pushed"},
		{"name": "ember-air", "enabled": true, "inLoop": true, "slot": 2, "present": true, "origin": "pushed"},
		{"name": "Date", "enabled": false, "inLoop": false, "slot": 3, "present": true, "origin": "builtin"},
		{"name": "Time", "enabled": true, "inLoop": true, "slot": 4, "present": true, "origin": "builtin"},
		{"name": "Temperature", "enabled": true, "inLoop": true, "slot": 5, "present": true, "origin": "builtin"},
		{"name": "Humidity", "enabled": false, "inLoop": false, "slot": 6, "present": true, "origin": "builtin"},
		{"name": "Battery", "enabled": false, "inLoop": false, "slot": 7, "present": true, "origin": "builtin"},
	}
}

func newArrangeClock(t *testing.T, apps []map[string]any) (*App, *clockStub) {
	t.Helper()
	a, _, stub := newArrangeClockServer(t, apps)
	return a, stub
}

func newArrangeClockServer(t *testing.T, apps []map[string]any) (*App, *httptest.Server, *clockStub) {
	t.Helper()
	a, srv, stub := newClockApp(t)
	stub.mu.Lock()
	stub.apps = apps
	stub.mu.Unlock()
	a.coord.publisher = clockPublisher{a.clock}
	a.coord.onPushed = a.arrangeClockApp
	t.Cleanup(a.clockJobs.Wait)
	return a, srv, stub
}

func pushTile(t *testing.T, a *App, app string) {
	t.Helper()
	a.coord.tiles.push(coordTileWriter{a.coord}, app, map[string]any{"text": app}, time.Now())
	a.clockJobs.Wait()
	if _, ok := a.coord.tiles.pushed[app]; !ok {
		t.Fatalf("%s not pushed", app)
	}
}

func notePush(a *App, app string) {
	a.coord.pushed(app)
	a.clockJobs.Wait()
}

func TestPrintAppGetsASlotInAStoredOrderThatPredatesIt(t *testing.T) {
	a, stub := newArrangeClock(t, liveLikeRotation())
	pushTile(t, a, "ember-print")
	if slot, _ := stub.slotOf("ember-print"); slot != 3 {
		t.Fatalf("ember-print slot = %v, want 3 (after ember-air); clock apps %v", slot, stub.apps)
	}
	puts := stub.puts()
	if len(puts) != 1 {
		t.Fatalf("order writes = %v, want one", puts)
	}
	got := decodeOrderPut(t, puts[0])
	wantOrder := []string{"ember", "ember-weather", "ember-air", "ember-print", "Date", "Time", "Temperature", "Humidity", "Battery"}
	if !slices.Equal(got.Order, wantOrder) {
		t.Fatalf("order = %v, want %v", got.Order, wantOrder)
	}
	if want := []string{"Date", "Humidity", "Battery"}; !slices.Equal(got.Disabled, want) {
		t.Fatalf("disabled = %v, want the clock's full off list %v", got.Disabled, want)
	}
	if rot := a.clockRotation.Load(); rot == nil || !slices.ContainsFunc(rot.listed, func(l clockListedApp) bool {
		return l.Name == "ember-print" && l.Slot != nil
	}) {
		t.Fatalf("rotation cache not refreshed after the write: %+v", rot)
	}
	notePush(a, "ember-print")
	pushTile(t, a, "ember-print-temp")
	puts = stub.puts()
	if len(puts) != 2 {
		t.Fatalf("order writes = %v, want a second one for ember-print-temp only", puts)
	}
	got = decodeOrderPut(t, puts[1])
	if i, j := slices.Index(got.Order, "ember-print"), slices.Index(got.Order, "ember-print-temp"); j != i+1 {
		t.Fatalf("order = %v, want ember-print-temp right after ember-print", got.Order)
	}
}

func TestBaseAppPushGetsASlotToo(t *testing.T) {
	apps := liveLikeRotation()[1:]
	for i, app := range apps {
		app["slot"] = i
	}
	a, stub := newArrangeClock(t, apps)
	notePush(a, "ember")
	if len(stub.puts()) != 0 {
		t.Fatalf("an app the clock doesn't list yet was arranged: %v", stub.puts())
	}
	stub.mu.Lock()
	stub.pushedLocked("ember")
	stub.mu.Unlock()
	notePush(a, "ember")
	if slot, _ := stub.slotOf("ember"); slot != 0 {
		t.Fatalf("ember slot = %v, want 0 (before ember-weather)", slot)
	}
}

func TestArrangeLeavesASwitchedOffAppOff(t *testing.T) {
	apps := append(liveLikeRotation(), map[string]any{"name": "ember-print", "enabled": false, "inLoop": false, "present": true, "origin": "pushed"})
	a, stub := newArrangeClock(t, apps)
	pushTile(t, a, "ember-print")
	if puts := stub.puts(); len(puts) != 0 {
		t.Fatalf("a switched-off app was arranged: %v", puts)
	}
	if on := stub.enabledByName()["ember-print"]; on != false {
		t.Fatalf("ember-print enabled = %v, want still off", on)
	}
}

func TestArrangeLeavesAClockWithoutAStoredOrder(t *testing.T) {
	apps := liveLikeRotation()
	for _, app := range apps {
		delete(app, "slot")
	}
	a, stub := newArrangeClock(t, apps)
	pushTile(t, a, "ember-print")
	if puts := stub.puts(); len(puts) != 0 {
		t.Fatalf("the clock's own default order was frozen: %v", puts)
	}
}

func TestArrangeRetriesAfterAFailedRead(t *testing.T) {
	a, stub := newArrangeClock(t, liveLikeRotation())
	now := time.Now()
	a.arranger.now = func() time.Time { return now }
	stub.mu.Lock()
	stub.appsFail = true
	stub.mu.Unlock()
	pushTile(t, a, "ember-print")
	stub.mu.Lock()
	stub.appsFail = false
	stub.mu.Unlock()
	notePush(a, "ember-print")
	if puts := stub.puts(); len(puts) != 0 {
		t.Fatalf("retried before the backoff: %v", puts)
	}
	now = now.Add(clockArrangeRetry)
	notePush(a, "ember-print")
	if slot, _ := stub.slotOf("ember-print"); slot != 3 {
		t.Fatalf("ember-print slot = %v after the retry, want 3", slot)
	}
}

func TestArrangeSkipsASettledAppUntilTheClockReboots(t *testing.T) {
	var g clockArranger
	pass := func(settle bool) {
		t.Helper()
		gen, names := g.take()
		var settled []string
		if settle {
			settled = names
		}
		if more := g.done(gen, names, settled, 0); !more {
			t.Fatal("a successful pass stopped the job loop")
		}
		if _, rest := g.take(); rest != nil {
			t.Fatalf("pending after the pass: %v", rest)
		}
	}
	if !g.want("http://clock", "ember-print") {
		t.Fatal("first want did not start a job")
	}
	pass(true)
	if g.want("http://clock", "ember-print") {
		t.Fatal("a settled app started another job")
	}
	if !g.want("http://other", "ember-print") {
		t.Fatal("a new clock URL kept the old clock's settled apps")
	}
	pass(true)
	g.forget()
	if !g.want("http://other", "ember-print") {
		t.Fatal("forget kept the settled apps")
	}
	pass(true)
	if !g.recheck() {
		t.Fatal("recheck did not queue the settled apps")
	}
	if _, names := g.take(); !slices.Equal(names, []string{"ember-print"}) {
		t.Fatalf("recheck queued %v, want [ember-print]", names)
	}
}

func TestArrangeIgnoresAPassFromBeforeAReboot(t *testing.T) {
	var g clockArranger
	g.want("http://clock", "ember-print")
	gen, names := g.take()
	g.forget()
	g.want("http://clock", "ember-air")
	g.done(gen, names, names, 0)
	if g.settled["ember-print"] {
		t.Fatal("a pass from before the reboot settled ember-print")
	}
}

func TestArrangeRetriesAPendingAppOnAnotherAppsPush(t *testing.T) {
	a, stub := newArrangeClock(t, liveLikeRotation())
	now := time.Now()
	a.arranger.now = func() time.Time { return now }
	stub.mu.Lock()
	stub.orderFail = true
	stub.mu.Unlock()
	pushTile(t, a, "ember-print")
	if n := len(stub.puts()); n != 1 {
		t.Fatalf("order writes = %d, want the one that failed", n)
	}
	stub.mu.Lock()
	stub.orderFail = false
	stub.mu.Unlock()
	now = now.Add(clockArrangeRetry)
	notePush(a, "ember")
	if slot, _ := stub.slotOf("ember-print"); slot != 3 {
		t.Fatalf("ember-print slot = %v after the base app's push, want 3", slot)
	}
}

func TestSwitchingAnOffAppOnGivesItASlot(t *testing.T) {
	apps := append(liveLikeRotation(), map[string]any{"name": "ember-print", "enabled": false, "inLoop": false, "present": true, "origin": "pushed"})
	a, srv, stub := newArrangeClockServer(t, apps)
	stub.version = "1.2.3"
	pushTile(t, a, "ember-print")
	if puts := stub.puts(); len(puts) != 0 {
		t.Fatalf("a switched-off app was arranged: %v", puts)
	}
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps/ember-print/enabled", testToken, `{"enabled":true}`)
	mustOK(t, "switch on", resp, b)
	a.clockJobs.Wait()
	if slot, _ := stub.slotOf("ember-print"); slot != 3 {
		t.Fatalf("ember-print slot = %v after it was switched on, want 3", slot)
	}
}

func TestArrangeKeepsScriptsWhileScriptingIsStopped(t *testing.T) {
	apps := append(liveLikeRotation(), map[string]any{"name": "ember_boot", "enabled": false, "inLoop": false, "present": true, "origin": "script"})
	a, stub := newArrangeClock(t, apps)
	now := time.Now()
	a.arranger.now = func() time.Time { return now }
	pushTile(t, a, "ember-print")
	if puts := stub.puts(); len(puts) != 0 {
		t.Fatalf("wrote the order while scripting may be stopped: %v", puts)
	}
	stub.mu.Lock()
	stub.scripting = boolPtr(false)
	stub.mu.Unlock()
	now = now.Add(clockArrangeScriptsWait)
	notePush(a, "ember")
	if puts := stub.puts(); len(puts) != 0 {
		t.Fatalf("wrote the order while scripting is stopped: %v", puts)
	}
	stub.mu.Lock()
	stub.scripting = boolPtr(true)
	stub.mu.Unlock()
	now = now.Add(clockArrangeScriptsWait)
	notePush(a, "ember")
	puts := stub.puts()
	if len(puts) != 1 {
		t.Fatalf("order writes = %v, want one once scripting runs", puts)
	}
	if got := decodeOrderPut(t, puts[0]); !slices.Contains(got.Disabled, "ember_boot") {
		t.Fatalf("disabled = %v, want the switched-off script kept off", got.Disabled)
	}
}

func TestArrangeWaitsForAnOrderChangeInFlight(t *testing.T) {
	a, stub := newArrangeClock(t, liveLikeRotation())
	stub.mu.Lock()
	stub.pushedLocked("ember-print")
	stub.mu.Unlock()
	release := a.holdClockRotation()
	a.coord.pushed("ember-print")
	time.Sleep(100 * time.Millisecond)
	stub.mu.Lock()
	gets := stub.appsGets
	stub.mu.Unlock()
	release()
	a.clockJobs.Wait()
	if gets != 0 {
		t.Fatalf("read the app list %d time(s) while another order change held the lock", gets)
	}
	if slot, _ := stub.slotOf("ember-print"); slot != 3 {
		t.Fatalf("ember-print slot = %v after the lock was released, want 3", slot)
	}
}

func TestOnlyTheClockPublisherArranges(t *testing.T) {
	t.Setenv("EMBER_CLOCK", "")
	cfg := defaultConfig()
	cfg.applyDefaults()
	if a := NewApp(cfg, nil, testLogger()); a.coord.onPushed == nil {
		t.Fatal("pushes to the clock don't arrange its order")
	}
	if a := NewApp(cfg, &recordingPublisher{}, testLogger()); a.coord.onPushed != nil {
		t.Fatal("a fake publisher would arrange the real clock's order")
	}
}

func TestArrangeEmberApps(t *testing.T) {
	ranks := emberAppRanks("ember")
	slot := func(n int) *int { return &n }
	on, off := boolPtr(true), boolPtr(false)
	cases := []struct {
		name    string
		rows    []clockAppRow
		apps    []string
		order   []string
		off     []string
		placed  []string
		settled []string
	}{
		{
			name: "named twice and reserved names keep their places",
			rows: []clockAppRow{
				{Name: "Time", Enabled: on, Origin: "builtin", Slot: slot(0)},
				{Name: "ember", Enabled: on, Origin: "pushed", Slot: slot(1)},
				{Name: "co2", Enabled: on, Slot: slot(2), Present: off},
				{Name: "Time", Enabled: on, Origin: "builtin", Slot: slot(3)},
				{Name: "mod", Origin: "module"},
				{Name: "ember-print", Enabled: on, Origin: "pushed"},
			},
			apps:    []string{"ember-print"},
			order:   []string{"Time", "ember", "ember-print", "co2", "Time"},
			off:     []string{},
			placed:  []string{"ember-print"},
			settled: []string{"ember-print"},
		},
		{
			name: "no Ember app arranged yet: joins at the end",
			rows: []clockAppRow{
				{Name: "Time", Enabled: on, Origin: "builtin", Slot: slot(0)},
				{Name: "Date", Enabled: off, Origin: "builtin"},
				{Name: "ember-weather", Enabled: on, Origin: "pushed"},
			},
			apps:    []string{"ember-weather"},
			order:   []string{"Time", "ember-weather"},
			off:     []string{"Date"},
			placed:  []string{"ember-weather"},
			settled: []string{"ember-weather"},
		},
		{
			name: "before the first later Ember app when no earlier one is arranged",
			rows: []clockAppRow{
				{Name: "Time", Enabled: on, Origin: "builtin", Slot: slot(0)},
				{Name: "ember-print", Enabled: on, Origin: "pushed", Slot: slot(1)},
				{Name: "ember-weather", Enabled: on, Origin: "pushed"},
			},
			apps:    []string{"ember-weather"},
			order:   []string{"Time", "ember-weather", "ember-print"},
			off:     []string{},
			placed:  []string{"ember-weather"},
			settled: []string{"ember-weather"},
		},
		{
			name: "several at once land in Ember's order",
			rows: []clockAppRow{
				{Name: "ember", Enabled: on, Origin: "pushed", Slot: slot(0)},
				{Name: "Time", Enabled: on, Origin: "builtin", Slot: slot(1)},
				{Name: "ember-print-pct", Enabled: on, Origin: "pushed"},
				{Name: "ember-print", Enabled: on, Origin: "pushed"},
			},
			apps:    []string{"ember-print-pct", "ember-print", "ember-gone"},
			order:   []string{"ember", "ember-print", "ember-print-pct", "Time"},
			off:     []string{},
			placed:  []string{"ember-print", "ember-print-pct"},
			settled: []string{"ember-print", "ember-print-pct"},
		},
		{
			name: "already arranged, switched off, or no stored order: nothing placed",
			rows: []clockAppRow{
				{Name: "ember", Enabled: on, Origin: "pushed", Slot: slot(0)},
				{Name: "ember-air", Enabled: off, Origin: "pushed"},
				{Name: "ember-meet", Origin: "pushed", Slot: slot(1)},
			},
			apps:    []string{"ember", "ember-air", "ember-meet"},
			order:   []string{"ember", "ember-meet"},
			off:     []string{"ember-air"},
			settled: []string{"ember", "ember-air", "ember-meet"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := arrangeEmberApps(tc.rows, tc.apps, ranks)
			if !slices.Equal(got.order.Order, tc.order) || !slices.Equal(got.order.Disabled, tc.off) {
				b, _ := json.Marshal(got.order)
				t.Fatalf("order = %s, want order %v disabled %v", b, tc.order, tc.off)
			}
			if !slices.Equal(got.placed, tc.placed) || !slices.Equal(got.settled, tc.settled) {
				t.Fatalf("placed %v settled %v, want %v %v", got.placed, got.settled, tc.placed, tc.settled)
			}
		})
	}
}
