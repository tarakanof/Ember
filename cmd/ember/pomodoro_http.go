package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
	"github.com/tarakanof/ember/internal/render"
)

var errPomodoroDisabled = errors.New("pomodoro feature is not enabled")

// defaultPomoMelody is a short RTTTL chime played at phase end when no custom
// melody is configured (stock TC001 piezo buzzer is RTTTL-only).
const defaultPomoMelody = "pomo:d=4,o=5,b=125:8g6,8c7,8e7"

// pomodoroSettingsDTO is the wire shape for GET/PUT /v1/pomodoro/config and the
// persisted-settings blob in the store. Every field is a pointer: nil means
// "omitted, leave the current value unchanged" (applyPomodoroSettings merges
// rather than replaces); dtoFromConfig always fills every pointer so GET
// responses and the persisted blob are fully resolved and round-trip byte-for-
// byte identically to the pre-pointer wire shape.
type pomodoroSettingsDTO struct {
	Enabled               *bool   `json:"enabled,omitempty"`
	FocusMinutes          *int    `json:"focus_minutes,omitempty"`
	ShortBreakMinutes     *int    `json:"short_break_minutes,omitempty"`
	LongBreakMinutes      *int    `json:"long_break_minutes,omitempty"`
	RoundsBeforeLongBreak *int    `json:"rounds_before_long_break,omitempty"`
	AutoStartNext         *bool   `json:"auto_start_next,omitempty"`
	Sound                 *bool   `json:"sound,omitempty"`
	SoundMelody           *string `json:"sound_melody,omitempty"`
	FocusColor            *string `json:"focus_color,omitempty"`
	BreakColor            *string `json:"break_color,omitempty"`
	MaxSessionMinutes     *int    `json:"max_session_minutes,omitempty"`
}

const pomodoroSettingsKey = "settings_json"

func dtoFromConfig(p PomodoroConfig) pomodoroSettingsDTO {
	return pomodoroSettingsDTO{
		Enabled:               &p.Enabled,
		FocusMinutes:          &p.FocusMinutes,
		ShortBreakMinutes:     &p.ShortBreakMinutes,
		LongBreakMinutes:      &p.LongBreakMinutes,
		RoundsBeforeLongBreak: &p.RoundsBeforeLongBreak,
		AutoStartNext:         &p.AutoStartNext,
		Sound:                 &p.Sound,
		SoundMelody:           &p.SoundMelody,
		FocusColor:            &p.FocusColor,
		BreakColor:            &p.BreakColor,
		MaxSessionMinutes:     &p.MaxSessionMinutes,
	}
}

func engineSettings(p PomodoroConfig) pomodoro.Settings {
	return pomodoro.Settings{
		FocusMin:         p.FocusMinutes,
		ShortMin:         p.ShortBreakMinutes,
		LongMin:          p.LongBreakMinutes,
		RoundsBeforeLong: p.RoundsBeforeLongBreak,
		AutoStartNext:    p.AutoStartNext,
		MaxSessionMin:    p.MaxSessionMinutes,
	}
}

// ensureStore opens the shared SQLite store at path (creating its directory)
// once, idempotently. The store backs Pomodoro stats AND the key/value settings
// used by hidden-apps and weather — so any of those features can
// open it independently of whether Pomodoro is enabled.
func (a *App) ensureStore(path string) error {
	if a.store != nil {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create store dir %q: %w", dir, err)
		}
	}
	store, err := pomodoro.Open(path)
	if err != nil {
		return err
	}
	a.store = store
	return nil
}

// initPomodoro opens the shared store, constructs the engine from config, wires
// both into the app, and re-applies any settings persisted by a previous run.
// Called unconditionally from main() — cfg.Pomodoro.Enabled only gates
// whether the engine actually runs, not whether it's wired up.
func (a *App) initPomodoro(p PomodoroConfig) error {
	if err := a.ensureStore(p.DBPath); err != nil {
		return err
	}
	engine := pomodoro.New(engineSettings(p), realClock{})
	a.EnablePomodoro(engine, a.store)
	a.loadPersistedPomodoroSettings()
	a.loadHiddenApps()
	return nil
}

// EnablePomodoro wires the engine + store into the app and connects the
// coordinator's preempt hook. Call once at startup when the feature is enabled.
func (a *App) EnablePomodoro(engine *pomodoro.Engine, store *pomodoro.Store) {
	a.engine = engine
	a.store = store
	a.coord.pomoView = a.pomoView
}

// pomodoroOn reports whether the Pomodoro feature is both available (engine
// wired — the store opened) and enabled (the runtime cfg flag, persisted). The
// engine is always wired at boot now; this flag gates the feature.
func (a *App) pomodoroOn() bool {
	return a.engine != nil && a.cfg.Load().Pomodoro.Enabled
}

// pomoView builds the render view for the coordinator from the live engine
// status and the configured colours. Returns active=false when idle/disabled.
func (a *App) pomoView() (render.PomodoroView, bool) {
	if !a.pomodoroOn() {
		return render.PomodoroView{}, false
	}
	st := a.engine.Status(time.Now())
	if st.Phase == pomodoro.PhaseIdle {
		return render.PomodoroView{}, false
	}
	p := a.cfg.Load().Pomodoro
	fc, _ := render.HexRGB(p.FocusColor)
	bc, _ := render.HexRGB(p.BreakColor)
	return render.PomodoroView{
		Phase:        string(st.Phase),
		Paused:       st.Paused,
		RemainingSec: st.RemainingSec,
		PlannedSec:   st.PlannedSec,
		FocusColor:   fc,
		BreakColor:   bc,
	}, true
}

// nudgePomo asks the coordinator to re-render promptly after a state change.
func (a *App) nudgePomo() {
	if a.coord != nil {
		a.coord.Send(coordCmd{kind: cmdTick})
	}
}

// pomoTick is called once per second by the coordinator loop. It advances the
// engine, records completed/elapsed phases, fires the phase-end alert, and
// nudges a re-render while a timer is active.
func (a *App) pomoTick() {
	if !a.pomodoroOn() {
		return
	}
	now := time.Now()
	if res := a.engine.Tick(now); res != nil {
		a.recordPhase(res, now)
		if res.Completed {
			a.pomoPhaseEndAlert(res)
		}
		a.nudgePomo()
		return
	}
	if a.engine.Active() {
		a.nudgePomo()
	}
}

func (a *App) recordPhase(res *pomodoro.PhaseResult, ended time.Time) {
	if a.store == nil {
		return
	}
	start := ended.Add(-time.Duration(res.ActualSec) * time.Second)
	if err := a.store.RecordPhase(*res, start, ended); err != nil {
		a.logger.Warn("pomodoro record phase failed", "err", err)
	}
}

// pomoPhaseEndAlert plays a notification (with a buzzer chime) when a phase
// completes, announcing the next phase.
func (a *App) pomoPhaseEndAlert(res *pomodoro.PhaseResult) {
	p := a.cfg.Load().Pomodoro
	if !p.Sound {
		return
	}
	text := "FOCUS"
	if res.Phase == pomodoro.PhaseFocus {
		text = "BREAK"
	}
	payload := map[string]any{"text": text, "wakeup": true, "durationMs": 4000, "stack": false}
	if p.SoundMelody != "" {
		payload["sound"] = p.SoundMelody
	} else {
		payload["soundRtttl"] = defaultPomoMelody
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.publisher.Notify(ctx, payload); err != nil {
		a.logger.Warn("pomodoro phase-end alert failed", "err", err)
	}
}

// writePomoState writes the current engine status as the JSON response body.
func (a *App) writePomoState(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, a.engine.Status(time.Now()))
}

func validPomodoroPhase(p pomodoro.Phase) bool {
	switch p {
	case pomodoro.PhaseFocus, pomodoro.PhaseShort, pomodoro.PhaseLong:
		return true
	default:
		return false
	}
}

func (a *App) handlePomodoroStart(w http.ResponseWriter, r *http.Request) {
	if !a.pomodoroOn() {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	var req struct {
		Phase string `json:"phase"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	phase := pomodoro.PhaseFocus
	if req.Phase != "" {
		phase = pomodoro.Phase(req.Phase)
		if !validPomodoroPhase(phase) {
			writeError(w, http.StatusBadRequest, errors.New("invalid phase"))
			return
		}
	}
	a.engine.Start(phase)
	a.nudgePomo()
	a.writePomoState(w)
}

func (a *App) handlePomodoroPause(w http.ResponseWriter, r *http.Request) {
	if !a.pomodoroOn() {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	a.engine.Pause(time.Now())
	a.nudgePomo()
	a.writePomoState(w)
}

func (a *App) handlePomodoroResume(w http.ResponseWriter, r *http.Request) {
	if !a.pomodoroOn() {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	a.engine.Resume(time.Now())
	a.nudgePomo()
	a.writePomoState(w)
}

func (a *App) handlePomodoroStop(w http.ResponseWriter, r *http.Request) {
	if !a.pomodoroOn() {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	now := time.Now()
	if res := a.engine.Stop(now); res != nil {
		a.recordPhase(res, now)
	}
	a.nudgePomo()
	a.writePomoState(w)
}

func (a *App) handlePomodoroSkip(w http.ResponseWriter, r *http.Request) {
	if !a.pomodoroOn() {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	now := time.Now()
	if res := a.engine.Skip(now); res != nil {
		a.recordPhase(res, now)
	}
	a.nudgePomo()
	a.writePomoState(w)
}

func (a *App) handlePomodoroState(w http.ResponseWriter, r *http.Request) {
	if !a.pomodoroOn() {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	a.writePomoState(w)
}

func (a *App) handlePomodoroConfigGet(w http.ResponseWriter, r *http.Request) {
	// Always available (incl. when disabled) so the app can show the Enable
	// toggle and the current settings.
	writeJSON(w, http.StatusOK, dtoFromConfig(a.cfg.Load().Pomodoro))
}

func (a *App) handlePomodoroConfigPut(w http.ResponseWriter, r *http.Request) {
	// No enabled-gate: this is how the app turns the feature on.
	var dto pomodoroSettingsDTO
	if err := decodeJSON(w, r, &dto, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.applyPomodoroSettings(dto); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, dtoFromConfig(a.cfg.Load().Pomodoro))
}

// applyPomodoroSettings merges the DTO onto the live config (nil fields keep
// their current value, matching how Enabled has always worked — a
// settings-only PUT or an older persisted blob can't accidentally zero fields
// it didn't intend to touch), validates the merged result, updates the
// engine, and persists it to the store for restart durability.
func (a *App) applyPomodoroSettings(dto pomodoroSettingsDTO) error {
	a.cfgMu.Lock()
	cur := *a.cfg.Load()
	p := cur.Pomodoro
	if dto.FocusMinutes != nil {
		p.FocusMinutes = *dto.FocusMinutes
	}
	if dto.ShortBreakMinutes != nil {
		p.ShortBreakMinutes = *dto.ShortBreakMinutes
	}
	if dto.LongBreakMinutes != nil {
		p.LongBreakMinutes = *dto.LongBreakMinutes
	}
	if dto.RoundsBeforeLongBreak != nil {
		p.RoundsBeforeLongBreak = *dto.RoundsBeforeLongBreak
	}
	if dto.AutoStartNext != nil {
		p.AutoStartNext = *dto.AutoStartNext
	}
	if dto.Sound != nil {
		p.Sound = *dto.Sound
	}
	if dto.SoundMelody != nil {
		p.SoundMelody = *dto.SoundMelody
	}
	if dto.FocusColor != nil {
		p.FocusColor = *dto.FocusColor
	}
	if dto.BreakColor != nil {
		p.BreakColor = *dto.BreakColor
	}
	if dto.MaxSessionMinutes != nil {
		p.MaxSessionMinutes = *dto.MaxSessionMinutes
	}
	if dto.Enabled != nil {
		p.Enabled = *dto.Enabled
	}
	if err := validatePomodoro(p); err != nil {
		a.cfgMu.Unlock()
		return err
	}
	cur.Pomodoro = p
	a.cfg.Store(&cur)
	a.cfgMu.Unlock()
	if a.engine != nil {
		a.engine.UpdateSettings(engineSettings(p))
		// An explicit disable must not strand a running timer on the clock.
		if dto.Enabled != nil && !*dto.Enabled && a.engine.Status(time.Now()).Phase != pomodoro.PhaseIdle {
			a.engine.Stop(time.Now())
		}
	}
	if a.store != nil {
		// Persist the resolved config (always carries enabled) so it survives a
		// restart even if the incoming DTO omitted enabled.
		if blob, err := json.Marshal(dtoFromConfig(p)); err == nil {
			if err := a.store.PutSetting(pomodoroSettingsKey, string(blob)); err != nil {
				a.logger.Warn("pomodoro settings persist failed", "err", err)
			}
		}
	}
	a.nudgePomo()
	// Provision any native icons the new config needs onto the device, off
	// the request path (it does device + gallery HTTP).
	go a.ensureNativeIcons(context.Background())
	return nil
}

// resyncPomodoroAfterReload re-aligns the engine with the freshly reloaded
// config and re-applies any settings persisted via the API, so a /admin/reload
// neither silently reverts runtime Pomodoro edits nor leaves the engine
// diverged from cfg. No-op when the feature is disabled.
func (a *App) resyncPomodoroAfterReload() {
	if a.engine == nil {
		return
	}
	a.engine.UpdateSettings(engineSettings(a.cfg.Load().Pomodoro))
	a.loadPersistedPomodoroSettings()
}

// loadPersistedPomodoroSettings applies any settings blob saved by a previous
// run on top of the file config, so menu-app edits survive restarts.
func (a *App) loadPersistedPomodoroSettings() {
	if a.store == nil {
		return
	}
	blob, ok, err := a.store.GetSetting(pomodoroSettingsKey)
	if err != nil || !ok {
		return
	}
	var dto pomodoroSettingsDTO
	if err := json.Unmarshal([]byte(blob), &dto); err != nil {
		a.logger.Warn("pomodoro persisted settings parse failed", "err", err)
		return
	}
	if err := a.applyPomodoroSettings(dto); err != nil {
		a.logger.Warn("pomodoro persisted settings invalid, ignoring", "err", err)
	}
}

// handleAwtrixButton ingests the awtrix-ng button callback: a plain-HTTP,
// fire-and-forget POST of `button=<left|middle|right>&state=<1|0>&uid=<mac>`,
// one per edge (press AND release). Unauthenticated by design — the device
// cannot send a bearer token — and answered immediately, because the firmware
// times out after 300 ms per edge on the display task and a slow reply shows up
// as visible stutter.
//
// `select` is kept as an accepted alias: NG's HTTP callback says "middle", but
// its own MQTT topics and Berry `on_button` hook call the same button "select",
// as did AWTRIX3. uid is deliberately ignored — it exists so several panels can
// share one endpoint, and Ember drives exactly one clock.
//
// Mapping: middle=pause/resume/start, left=stop, right=skip — all on press;
// releases are ignored.
func (a *App) handleAwtrixButton(w http.ResponseWriter, r *http.Request) {
	// Record receipt before any early-return: a POST landing here at all proves
	// the clock's button_callback is configured + reaching us (surfaced via
	// GET /v1/device/buttons), independent of whether Pomodoro acts on it.
	a.lastButtonAt.Store(time.Now().Unix())
	if !a.pomodoroOn() || !a.cfg.Load().Pomodoro.ButtonCallback {
		w.WriteHeader(http.StatusOK) // accept-and-ignore; device keeps posting
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	now := time.Now()
	button := r.PostFormValue("button")
	down := r.PostFormValue("state") == "1"

	// While a hold:true reminder alarm is on the clock, any button edge is the
	// user acknowledging it — not a Pomodoro action; a middle/select press
	// disarms the window.
	//
	// The server's own DELETE is belt-and-braces: contrary to the AWTRIX3-era
	// assumption, NG documents that a configured buttonCallback does NOT consume
	// the press ("the buttons keep their normal job"), and that a select press
	// dismisses the showing notification regardless — even with blockNavigation
	// set. So the firmware has very likely cleared it already and this DELETE is
	// a no-op (it answers 200 even when nothing is showing). It is kept because
	// the failure mode of being wrong the other way is a hold:true alarm stuck on
	// the clock forever; the cost of being right is that a second notification
	// queued behind the alarm can be skipped. Only a physical-button test settles
	// which happens on 1.0.13.
	if held := a.reminderHeldUntil.Load(); held != 0 && now.UnixNano() < held {
		if down && (button == "middle" || button == "select") {
			a.reminderHeldUntil.Store(0)
			if a.publisher != nil {
				if err := a.publisher.DismissNotify(r.Context()); err != nil {
					a.logger.Warn("reminder dismiss failed", "err", err)
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	acted := false
	switch button {
	case "middle", "select":
		if down {
			a.pomoMiddlePress(now)
			acted = true
		}
	case "left", "right":
		acted = a.pomoSideButton(button, down, now)
	}
	if acted {
		a.nudgePomo()
	}
	w.WriteHeader(http.StatusOK)
}

// pomoMiddlePress is the middle/select play-pause: running→pause, idle→start
// focus, paused/parked→resume.
func (a *App) pomoMiddlePress(now time.Time) {
	st := a.engine.Status(now)
	switch {
	case st.Running && !st.Paused:
		a.engine.Pause(now)
	case st.Phase == pomodoro.PhaseIdle:
		a.engine.Start(pomodoro.PhaseFocus)
	default:
		a.engine.Resume(now)
	}
}

// pomoSideButton processes a left/right edge: left=stop, right=skip, both on
// press. Releases are ignored. Returns whether the engine state changed.
func (a *App) pomoSideButton(button string, down bool, now time.Time) bool {
	if !down {
		return false
	}
	switch button {
	case "right":
		if res := a.engine.Skip(now); res != nil {
			a.recordPhase(res, now)
		}
	case "left":
		if res := a.engine.Stop(now); res != nil {
			a.recordPhase(res, now)
		}
	}
	return true
}
