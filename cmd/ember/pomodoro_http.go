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
// persisted-settings blob in the store.
type pomodoroSettingsDTO struct {
	FocusMinutes          int    `json:"focus_minutes"`
	ShortBreakMinutes     int    `json:"short_break_minutes"`
	LongBreakMinutes      int    `json:"long_break_minutes"`
	RoundsBeforeLongBreak int    `json:"rounds_before_long_break"`
	AutoStartNext         bool   `json:"auto_start_next"`
	Sound                 bool   `json:"sound"`
	SoundMelody           string `json:"sound_melody"`
	FocusColor            string `json:"focus_color"`
	BreakColor            string `json:"break_color"`
	MaxSessionMinutes     int    `json:"max_session_minutes"`
}

const pomodoroSettingsKey = "settings_json"

func dtoFromConfig(p PomodoroConfig) pomodoroSettingsDTO {
	return pomodoroSettingsDTO{
		FocusMinutes:          p.FocusMinutes,
		ShortBreakMinutes:     p.ShortBreakMinutes,
		LongBreakMinutes:      p.LongBreakMinutes,
		RoundsBeforeLongBreak: p.RoundsBeforeLongBreak,
		AutoStartNext:         p.AutoStartNext,
		Sound:                 p.Sound,
		SoundMelody:           p.SoundMelody,
		FocusColor:            p.FocusColor,
		BreakColor:            p.BreakColor,
		MaxSessionMinutes:     p.MaxSessionMinutes,
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
// Called from main() when the feature is enabled.
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

// pomoView builds the render view for the coordinator from the live engine
// status and the configured colours. Returns active=false when idle/disabled.
func (a *App) pomoView() (render.PomodoroView, bool) {
	if a.engine == nil {
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
	if a.engine == nil {
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
	payload := map[string]any{"text": text, "wakeup": true, "duration": 4, "stack": false}
	if p.SoundMelody != "" {
		payload["sound"] = p.SoundMelody
	} else {
		payload["rtttl"] = defaultPomoMelody
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
	if a.engine == nil {
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
	if a.engine == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	a.engine.Pause(time.Now())
	a.nudgePomo()
	a.writePomoState(w)
}

func (a *App) handlePomodoroResume(w http.ResponseWriter, r *http.Request) {
	if a.engine == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	a.engine.Resume(time.Now())
	a.nudgePomo()
	a.writePomoState(w)
}

func (a *App) handlePomodoroStop(w http.ResponseWriter, r *http.Request) {
	if a.engine == nil {
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
	if a.engine == nil {
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
	if a.engine == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	a.writePomoState(w)
}

func (a *App) handlePomodoroStats(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	now := time.Now()
	today, err := a.store.Today(now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	hist, err := a.store.History(now, 7)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	streak, err := a.store.Streak(now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"today":   today,
		"history": hist,
		"streak":  streak,
	})
}

func (a *App) handlePomodoroConfigGet(w http.ResponseWriter, r *http.Request) {
	if a.engine == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	writeJSON(w, http.StatusOK, dtoFromConfig(a.cfg.Load().Pomodoro))
}

func (a *App) handlePomodoroConfigPut(w http.ResponseWriter, r *http.Request) {
	if a.engine == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
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

// applyPomodoroSettings validates the DTO, swaps it into the live config,
// updates the engine, and persists it to the store for restart durability.
func (a *App) applyPomodoroSettings(dto pomodoroSettingsDTO) error {
	cur := *a.cfg.Load()
	p := cur.Pomodoro
	p.FocusMinutes = dto.FocusMinutes
	p.ShortBreakMinutes = dto.ShortBreakMinutes
	p.LongBreakMinutes = dto.LongBreakMinutes
	p.RoundsBeforeLongBreak = dto.RoundsBeforeLongBreak
	p.AutoStartNext = dto.AutoStartNext
	p.Sound = dto.Sound
	p.SoundMelody = dto.SoundMelody
	p.FocusColor = dto.FocusColor
	p.BreakColor = dto.BreakColor
	p.MaxSessionMinutes = dto.MaxSessionMinutes
	if err := validatePomodoro(p); err != nil {
		return err
	}
	cur.Pomodoro = p
	a.cfg.Store(&cur)
	if a.engine != nil {
		a.engine.UpdateSettings(engineSettings(p))
	}
	if a.store != nil {
		if blob, err := json.Marshal(dto); err == nil {
			if err := a.store.PutSetting(pomodoroSettingsKey, string(blob)); err != nil {
				a.logger.Warn("pomodoro settings persist failed", "err", err)
			}
		}
	}
	a.nudgePomo()
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

// handleAwtrixButton ingests device button-callback POSTs (form-encoded:
// button, state, uid). Unauthenticated by design — the device cannot send a
// bearer token. Acts only on press-down (state=1): middle=pause/resume,
// right=skip, left=stop.
func (a *App) handleAwtrixButton(w http.ResponseWriter, r *http.Request) {
	if a.engine == nil || !a.cfg.Load().Pomodoro.ButtonCallback {
		w.WriteHeader(http.StatusOK) // accept-and-ignore; device keeps posting
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PostFormValue("state") != "1" {
		w.WriteHeader(http.StatusOK) // ignore release edges
		return
	}
	now := time.Now()
	switch r.PostFormValue("button") {
	case "middle", "select":
		st := a.engine.Status(now)
		switch {
		case st.Running && !st.Paused:
			a.engine.Pause(now)
		case st.Phase == pomodoro.PhaseIdle:
			a.engine.Start(pomodoro.PhaseFocus) // idle → begin a focus block
		default:
			a.engine.Resume(now) // paused or parked → resume/start
		}
	case "right":
		if res := a.engine.Skip(now); res != nil {
			a.recordPhase(res, now)
		}
	case "left":
		if res := a.engine.Stop(now); res != nil {
			a.recordPhase(res, now)
		}
	}
	a.nudgePomo()
	w.WriteHeader(http.StatusOK)
}
