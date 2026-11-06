package main

import (
	"net/http"
	"sync"
	"testing"

	"github.com/tarakanof/ember/internal/awtrix"
)

type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKV) GetSetting(key string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return v, ok, nil
}

func (k *memKV) PutSetting(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = value
	return nil
}

func (k *memKV) get(key string) string {
	v, _, _ := k.GetSetting(key)
	return v
}

func wantTakeoverSettings(t *testing.T, got map[string]any, autoTransition, blockNavigation bool) {
	t.Helper()
	if got["autoTransition"] != autoTransition || got["blockNavigation"] != blockNavigation {
		t.Fatalf("settings = %v, want autoTransition:%v blockNavigation:%v", got, autoTransition, blockNavigation)
	}
}

func TestCoordinatorTakeoverRestoresUsersPriorSettings(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false, "brightness": 90.0}

	*pomo = true
	c.publish(*snap)
	*pomo = false
	c.publish(*snap)

	s := pub.SettingsSnapshot()
	if len(s) != 2 {
		t.Fatalf("settings writes = %+v, want takeover + restore", s)
	}
	wantTakeoverSettings(t, s[0], false, true)
	wantTakeoverSettings(t, s[1], false, false)
	if len(s[1]) != 2 {
		t.Fatalf("restore payload = %v, want only the two takeover keys", s[1])
	}
}

func TestCoordinatorTakeoverWaitsForASuccessfulSnapshotRead(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.readSettingsErr = errUnreachableDevice
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false}

	*pomo = true
	c.publish(*snap)
	if s := pub.SettingsSnapshot(); len(s) != 0 {
		t.Fatalf("settings written with no snapshot = %+v, want none", s)
	}
	if c.hold != holdNone {
		t.Fatalf("hold = %v, want holdNone until the takeover lands", c.hold)
	}

	pub.mu.Lock()
	pub.readSettingsErr = nil
	pub.mu.Unlock()
	c.publish(*snap)
	*pomo = false
	c.publish(*snap)

	s := pub.SettingsSnapshot()
	if len(s) != 2 {
		t.Fatalf("settings writes = %+v, want takeover + restore", s)
	}
	wantTakeoverSettings(t, s[1], false, false)
}

func TestCoordinatorTakeoverIgnoresSnapshotOfTheTakeoverItself(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": true}

	*pomo = true
	c.publish(*snap)
	*pomo = false
	c.publish(*snap)

	s := pub.SettingsSnapshot()
	if len(s) != 2 {
		t.Fatalf("settings writes = %+v, want takeover + restore", s)
	}
	wantTakeoverSettings(t, s[1], true, false)
}

func TestCoordinatorBacksOffPendingRestoreWhileClockOffline(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	*pomo = true
	c.publish(*snap)
	base := len(pub.SettingsSnapshot())

	pub.mu.Lock()
	pub.settingsFails = 100
	pub.mu.Unlock()
	*pomo = false
	c.publish(*snap)
	afterFirst := len(pub.SettingsSnapshot())
	if afterFirst == base {
		t.Fatal("no restore attempt on the release edge")
	}

	for i := 0; i < restoreBackoffTicks; i++ {
		c.publish(*snap)
	}
	if n := len(pub.SettingsSnapshot()); n != afterFirst {
		t.Fatalf("restore attempts during backoff = %d, want 0", n-afterFirst)
	}

	pub.mu.Lock()
	pub.settingsFails = 0
	pub.mu.Unlock()
	c.publish(*snap)
	if c.hold != holdNone || c.prior != nil {
		t.Fatalf("after backoff: hold=%v prior=%v, want restored", c.hold, c.prior)
	}
	if c.restoreBackoff != 0 {
		t.Fatalf("restoreBackoff after success = %d, want 0", c.restoreBackoff)
	}
}

func TestCoordinatorTakeoverFallsBackToDefaultsOnRejectedRead(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.readSettingsErr = &awtrix.APIError{StatusCode: http.StatusNotFound}

	*pomo = true
	c.publish(*snap)
	*pomo = false
	c.publish(*snap)

	s := pub.SettingsSnapshot()
	if len(s) != 2 {
		t.Fatalf("settings writes = %+v, want takeover + restore", s)
	}
	wantTakeoverSettings(t, s[1], true, false)
}

func TestCoordinatorRetriesFailedHoldSwitch(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "waiting")
	c.locked, c.lockedKey, c.pointer = true, "mbp/claude/a", "mbp/claude/a"
	pub.switchFails = publishAttempts

	c.publish(*snap)
	if c.hold != holdNone {
		t.Fatalf("hold after a lost switch = %v, want holdNone", c.hold)
	}

	c.publish(*snap)
	if c.hold != holdAttention {
		t.Fatalf("hold after the retry = %v, want holdAttention", c.hold)
	}
	if n := len(pub.SwitchesSnapshot()); n != publishAttempts+1 {
		t.Fatalf("switch attempts = %d, want %d", n, publishAttempts+1)
	}
}

func TestCoordinatorRetriesHoldSwitchWithinTheTick(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "waiting")
	c.locked, c.lockedKey, c.pointer = true, "mbp/claude/a", "mbp/claude/a"
	pub.switchFails = 1

	c.publish(*snap)
	if c.hold != holdAttention {
		t.Fatalf("hold = %v, want holdAttention after an in-tick retry", c.hold)
	}
}

func TestCoordinatorRetriesFailedTakeoverSettings(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.settingsFails = publishAttempts

	*pomo = true
	c.publish(*snap)
	if c.hold != holdNone {
		t.Fatalf("hold after a lost takeover = %v, want holdNone", c.hold)
	}
	if n := len(pub.SwitchesSnapshot()); n != 0 {
		t.Fatalf("switches before the takeover landed = %d, want 0", n)
	}

	c.publish(*snap)
	if c.hold != holdPomodoro {
		t.Fatalf("hold after the retry = %v, want holdPomodoro", c.hold)
	}
}

func TestCoordinatorTakeoverLandsAfterClockOutageWithoutRepublish(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	fail := &failingCustomAppPublisher{recordingPublisher: pub, fail: true}
	c.publisher = fail
	pub.readSettingsErr = errUnreachableDevice
	pub.settingsFails = 100
	pub.switchFails = 100
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false}

	*pomo = true
	for range 3 {
		c.publish(*snap)
	}
	fail.fail = false
	c.publish(*snap)
	if c.hold != holdNone {
		t.Fatalf("hold with the clock unreachable = %v, want holdNone", c.hold)
	}

	pub.mu.Lock()
	pub.readSettingsErr, pub.settingsFails, pub.switchFails = nil, 0, 0
	pub.mu.Unlock()
	c.publish(*snap)
	if c.hold != holdPomodoro {
		t.Fatalf("hold once the clock answers = %v, want holdPomodoro", c.hold)
	}
	s := pub.SettingsSnapshot()
	wantTakeoverSettings(t, s[len(s)-1], false, true)

	*pomo = false
	c.publish(*snap)
	s = pub.SettingsSnapshot()
	wantTakeoverSettings(t, s[len(s)-1], false, false)
}

func TestCoordinatorRetriesFailedTakeoverRestore(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	*pomo = true
	c.publish(*snap)

	pub.mu.Lock()
	pub.settingsFails = publishAttempts
	pub.mu.Unlock()
	*pomo = false
	c.publish(*snap)
	if c.hold != holdPomodoro {
		t.Fatalf("hold after a lost restore = %v, want holdPomodoro (restore pending)", c.hold)
	}

	for i := 0; i <= restoreBackoffTicks; i++ {
		c.publish(*snap)
	}
	if c.hold != holdNone {
		t.Fatalf("hold after the restore retry = %v, want holdNone", c.hold)
	}
	s := pub.SettingsSnapshot()
	wantTakeoverSettings(t, s[len(s)-1], true, false)
}

func TestCoordinatorRestoresHalfAppliedTakeover(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.switchFails = publishAttempts

	*pomo = true
	c.publish(*snap)
	*pomo = false
	c.publish(*snap)

	s := pub.SettingsSnapshot()
	if len(s) != 2 {
		t.Fatalf("settings writes = %+v, want takeover + restore", s)
	}
	wantTakeoverSettings(t, s[1], true, false)
}

func TestCoordinatorPersistsTakeoverSnapshotAcrossRestart(t *testing.T) {
	kv := &memKV{}
	c, pub, snap, pomo := holdFixture(t, "running")
	c.setSettingsKV(kv)
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false}

	*pomo = true
	c.publish(*snap)
	if kv.get(takeoverPriorKey) == "" {
		t.Fatal("takeover snapshot not persisted")
	}

	c2, pub2, snap2, _ := holdFixture(t, "running")
	c2.setSettingsKV(kv)
	c2.publish(*snap2)

	s := pub2.SettingsSnapshot()
	if len(s) != 1 {
		t.Fatalf("settings on the first publish after restart = %+v, want one restore", s)
	}
	wantTakeoverSettings(t, s[0], false, false)
	if v := kv.get(takeoverPriorKey); v != "" {
		t.Fatalf("snapshot after restore = %q, want cleared", v)
	}
}

func TestCoordinatorExitRestoreKeepsSnapshotWhenDeviceUnreachable(t *testing.T) {
	kv := &memKV{}
	c, pub, snap, pomo := holdFixture(t, "running")
	c.setSettingsKV(kv)
	*pomo = true
	c.publish(*snap)

	pub.mu.Lock()
	pub.settingsFails = 100
	pub.mu.Unlock()
	c.restorePomoTakeoverOnExit()

	if kv.get(takeoverPriorKey) == "" {
		t.Fatal("snapshot cleared although the exit restore failed")
	}

	pub.mu.Lock()
	pub.settingsFails = 0
	pub.mu.Unlock()
	c.restorePomoTakeoverOnExit()
	if v := kv.get(takeoverPriorKey); v != "" {
		t.Fatalf("snapshot after a successful exit restore = %q, want cleared", v)
	}
}

func TestEnsureStoreLoadsLeftoverTakeoverSnapshot(t *testing.T) {
	path := t.TempDir() + "/s.db"
	first := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if err := first.ensureStore(path); err != nil {
		t.Fatal(err)
	}
	if err := first.store.PutSetting(takeoverPriorKey, `{"autoTransition":false,"blockNavigation":false}`); err != nil {
		t.Fatal(err)
	}
	if err := first.store.Close(); err != nil {
		t.Fatal(err)
	}

	next := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if err := next.ensureStore(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.store.Close() })
	if p := next.coord.prior; p == nil || p.AutoTransition || p.BlockNavigation {
		t.Fatalf("prior after restart = %+v, want the persisted snapshot", p)
	}
}

func TestCoordinatorRepublishKeepsOriginalTakeoverSnapshot(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false}
	*pomo = true
	c.publish(*snap)

	pub.mu.Lock()
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": true}
	pub.mu.Unlock()
	c.hold = holdNone
	c.mainPushed = pushedApp{}
	c.publish(*snap)

	*pomo = false
	c.publish(*snap)
	s := pub.SettingsSnapshot()
	wantTakeoverSettings(t, s[len(s)-1], false, false)
}
