package main

import (
	"regexp"
	"strings"
	"sync"

	"github.com/tarakanof/ember/internal/devices"
)

var clockUIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

func normalizeClockUID(raw string) (string, bool) {
	s := strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(strings.TrimSpace(raw)))
	return s, clockUIDPattern.MatchString(s)
}

func (a *App) observeClock(rawUID string, seen *devices.ClockSeen) {
	uid, ok := normalizeClockUID(rawUID)
	if !ok || a.devices == nil || a.devices.LoadError() != nil {
		return
	}
	view, created, err := a.devices.SeenClock(uid, seen, clockConfigDigest(a.composeClockConfig()))
	if err != nil {
		a.logger.Warn("clock record not stored", "uid", uid, "err", err)
		return
	}
	if created {
		a.logger.Info("clock registered", "device_id", view.ID, "uid", uid)
	}
	if created || a.clockMigrate.lastError() != nil {
		a.migrateClockConfigInBackground()
	}
}

type clockSyncGate struct {
	mu     sync.Mutex
	paused int
}

func (a *App) syncClockConfigVersion() {
	if a.devices == nil {
		return
	}
	a.clockSync.mu.Lock()
	defer a.clockSync.mu.Unlock()
	if a.clockSync.paused > 0 {
		return
	}
	a.syncClockConfigLocked(a.composeClockConfig())
}

func (a *App) syncClockConfigLocked(cfg clockConfig) {
	changed, err := a.devices.SyncClockConfig(clockConfigDigest(cfg), clockConfigHash(cfg))
	if err != nil {
		a.logger.Warn("clock config version not stored", "err", err)
		return
	}
	if changed {
		a.logger.Debug("clock config version changed")
	}
}

func (a *App) clockConfigSnapshot() (clockConfig, int) {
	a.clockSync.mu.Lock()
	defer a.clockSync.mu.Unlock()
	cfg := a.composeClockConfig()
	if a.devices != nil && a.clockSync.paused == 0 {
		a.syncClockConfigLocked(cfg)
	}
	return cfg, a.clockConfigVersion()
}

func (a *App) pauseClockSync() {
	a.clockSync.mu.Lock()
	a.clockSync.paused++
	a.clockSync.mu.Unlock()
}

func (a *App) resumeClockSync() {
	a.clockSync.mu.Lock()
	a.clockSync.paused--
	a.clockSync.mu.Unlock()
	a.syncClockConfigVersion()
}

func (a *App) reapplySettings() {
	a.pauseClockSync()
	defer a.resumeClockSync()
	a.iconHold.Add(1)
	defer a.iconHold.Add(-1)
	a.settings.reapply()
}
