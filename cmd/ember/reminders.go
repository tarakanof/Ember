package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// RemindersConfig holds the configured alarms. Reminders are evaluated in the
// configured IANA timezone (the container runs UTC), so HH:MM means the user's
// local time. Each reminder fires a bell-icon popup at its time, at most once per
// day. Runtime-editable from the menu (persisted to the store).
type RemindersConfig struct {
	Enabled              bool       `json:"enabled"`
	Timezone             string     `json:"timezone"` // IANA name; empty = UTC
	PopupDurationSeconds int        `json:"popup_duration_seconds"`
	UseNativeIcon        bool       `json:"use_native_icon"`
	NativeIconID         string     `json:"native_icon_id"`
	Items                []Reminder `json:"items"`
}

// Reminder is one alarm. Days is a set of weekdays (0=Sun … 6=Sat); empty means
// every day. Time is "HH:MM" 24-hour.
type Reminder struct {
	ID      string `json:"id"`
	Time    string `json:"time"`
	Text    string `json:"text"`
	Days    []int  `json:"days"`
	Enabled bool   `json:"enabled"`
	Sound   bool   `json:"sound"`
}

// defaultReminderSound is a short gentle RTTTL chime for reminders that opt into
// sound (the TC001 piezo is RTTTL-only).
const defaultReminderSound = "remind:d=4,o=6,b=140:8e,8g,8c7"

func (c *RemindersConfig) applyDefaults() {
	if c.PopupDurationSeconds <= 0 {
		c.PopupDurationSeconds = 8
	}
	if c.Items == nil {
		c.Items = []Reminder{}
	}
}

// parseHHMM parses "HH:MM" (24h) into hour, minute. ok=false on malformed input.
func parseHHMM(s string) (h, m int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func validateReminders(c RemindersConfig) error {
	if c.Timezone != "" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return fmt.Errorf("reminders.timezone %q invalid: %w", c.Timezone, err)
		}
	}
	if c.PopupDurationSeconds < 1 || c.PopupDurationSeconds > 300 {
		return fmt.Errorf("reminders.popup_duration_seconds must be 1..300")
	}
	seen := map[string]bool{}
	for i, it := range c.Items {
		if strings.TrimSpace(it.ID) == "" {
			return fmt.Errorf("reminders.items[%d].id is required", i)
		}
		if seen[it.ID] {
			return fmt.Errorf("reminders.items[%d].id %q is duplicated", i, it.ID)
		}
		seen[it.ID] = true
		if _, _, ok := parseHHMM(it.Time); !ok {
			return fmt.Errorf("reminders.items[%d].time %q must be HH:MM", i, it.Time)
		}
		if strings.TrimSpace(it.Text) == "" {
			return fmt.Errorf("reminders.items[%d].text is required", i)
		}
		for _, d := range it.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("reminders.items[%d].days has out-of-range weekday %d", i, d)
			}
		}
	}
	return nil
}

// reminderStore tracks the last calendar date (in the configured tz) each
// reminder fired, so a reminder fires at most once per day even though the ticker
// runs every 30s.
type reminderStore struct {
	mu        sync.Mutex
	lastFired map[string]string // reminder ID -> "YYYY-MM-DD"
}

func newReminderStore() *reminderStore {
	return &reminderStore{lastFired: map[string]string{}}
}

// shouldFire reports whether reminder id should fire for date today; records the
// fire when it returns true (so a second call the same day returns false).
func (s *reminderStore) shouldFire(id, today string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastFired[id] == today {
		return false
	}
	s.lastFired[id] = today
	return true
}

// pruneTo drops last-fired bookkeeping for any reminder id not in keep, bounding
// the map across menu edits (each add mints a new UUID).
func (s *reminderStore) pruneTo(keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.lastFired {
		if !keep[id] {
			delete(s.lastFired, id)
		}
	}
}

func reminderMatchesDay(r Reminder, weekday int) bool {
	if len(r.Days) == 0 {
		return true
	}
	for _, d := range r.Days {
		if d == weekday {
			return true
		}
	}
	return false
}

// StartReminders runs the reminder evaluation loop until ctx is done, ticking
// every 30s. A no-op while the feature is disabled.
func (a *App) StartReminders(ctx context.Context) {
	if a.reminders == nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	a.evalReminders(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.evalReminders(ctx, time.Now())
		}
	}
}

// evalReminders fires any due reminders for the instant `now`. The `now`
// parameter makes it deterministically testable.
func (a *App) evalReminders(ctx context.Context, now time.Time) {
	cfg := a.cfg.Load().Reminders
	if !cfg.Enabled {
		return
	}
	loc := time.UTC
	if cfg.Timezone != "" {
		if l, err := time.LoadLocation(cfg.Timezone); err == nil {
			loc = l
		} else {
			a.logger.Warn("reminders timezone invalid, using UTC", "tz", cfg.Timezone, "err", err)
		}
	}
	local := now.In(loc)
	today := local.Format("2006-01-02")
	weekday := int(local.Weekday())
	// Drop bookkeeping for reminders that no longer exist (the menu mints a fresh
	// UUID per add), so lastFired can't grow unbounded across edits.
	ids := make(map[string]bool, len(cfg.Items))
	for _, r := range cfg.Items {
		ids[r.ID] = true
	}
	a.reminders.pruneTo(ids)
	for _, r := range cfg.Items {
		if !r.Enabled {
			continue
		}
		h, m, ok := parseHHMM(r.Time)
		if !ok {
			continue
		}
		// Fire within a short grace window after the target rather than on exact
		// minute equality: the 30s ticker coalesces missed ticks (host sleep, GC
		// pause), so requiring a tick in the exact wall-clock minute would silently
		// drop the alarm. The once-per-day shouldFire guard keeps it from repeating
		// across the window. A stale alarm (delta > grace, e.g. waking hours later)
		// is intentionally NOT fired.
		target := time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc)
		delta := local.Sub(target)
		if delta < 0 || delta > reminderGrace {
			continue
		}
		if !reminderMatchesDay(r, weekday) {
			continue
		}
		if !a.reminders.shouldFire(r.ID, today) {
			continue
		}
		a.fireReminder(ctx, r, cfg)
	}
}

// reminderGrace bounds how late a reminder may fire after its target minute, so
// a coalesced/missed 30s tick doesn't drop it. Wide enough to cover a couple of
// missed ticks, narrow enough that a long host sleep doesn't fire a stale alarm.
const reminderGrace = 90 * time.Second

func (a *App) fireReminder(ctx context.Context, r Reminder, cfg RemindersConfig) {
	sound := ""
	if r.Sound {
		sound = defaultReminderSound
	}
	iconID := ""
	if cfg.UseNativeIcon {
		iconID = cfg.NativeIconID
	}
	payload := render.ReminderPopupPayload(r.Text, iconID, cfg.PopupDurationSeconds, sound)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.publisher.Notify(cctx, payload); err != nil {
		a.logger.Warn("reminder popup failed", "id", r.ID, "err", err)
	}
}

// ---- config persistence ----

const remindersSettingsKey = "reminders_json"

func (a *App) applyReminderSettings(cfg RemindersConfig) error {
	cfg.applyDefaults()
	if err := validateReminders(cfg); err != nil {
		return err
	}
	cur := *a.cfg.Load()
	cur.Reminders = cfg
	a.cfg.Store(&cur)
	if a.store != nil {
		if blob, err := json.Marshal(cfg); err == nil {
			if err := a.store.PutSetting(remindersSettingsKey, string(blob)); err != nil {
				a.logger.Warn("reminders settings persist failed", "err", err)
			}
		}
	}
	return nil
}

func (a *App) loadPersistedReminderSettings() {
	if a.store == nil {
		return
	}
	blob, ok, err := a.store.GetSetting(remindersSettingsKey)
	if err != nil || !ok {
		return
	}
	var cfg RemindersConfig
	if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
		a.logger.Warn("reminders persisted settings parse failed", "err", err)
		return
	}
	if err := a.applyReminderSettings(cfg); err != nil {
		a.logger.Warn("reminders persisted settings invalid, ignoring", "err", err)
	}
}
