package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
	"github.com/tarakanof/ember/internal/render"
)

var errPomodoroDisabled = errors.New("pomodoro feature is not enabled")

const defaultPomoMelody = "pomo:d=4,o=5,b=125:8g6,8c7,8e7"

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
	DailyGoalSessions     *int    `json:"daily_goal_sessions,omitempty"`
	WeeklyGoalDays        *int    `json:"weekly_goal_days,omitempty"`
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
		DailyGoalSessions:     &p.DailyGoalSessions,
		WeeklyGoalDays:        &p.WeeklyGoalDays,
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
	a.coord.setSettingsKV(store)
	return nil
}

func (a *App) initPomodoro(p PomodoroConfig) error {
	if err := a.ensureStore(p.DBPath); err != nil {
		return err
	}
	engine := pomodoro.New(engineSettings(p), realClock{})
	a.EnablePomodoro(engine, a.store)
	a.loadHiddenApps()
	if err := a.devices.load(); err != nil {
		a.logger.Warn("device registry ignored", "err", err)
	}
	return nil
}

// EnablePomodoro wires the engine + store into the app and connects the coordinator's preempt hook.
func (a *App) EnablePomodoro(engine *pomodoro.Engine, store *pomodoro.Store) {
	a.engine = engine
	a.store = store
	a.coord.pomoView = a.pomoView
}

func (a *App) pomodoroOn() bool {
	return a.engine != nil && a.cfg.Load().Pomodoro.Enabled
}

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

func (a *App) nudgePomo() {
	if a.coord != nil {
		a.coord.Send(coordCmd{kind: cmdTick})
	}
}

// pomoChanged tells pull clients and the coordinator that the engine moved.
func (a *App) pomoChanged() {
	a.changes.notify(topicPomodoro)
	a.nudgePomo()
}

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
		a.pomoChanged()
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

func (a *App) pomoPhaseEndAlert(res *pomodoro.PhaseResult) {
	p := a.cfg.Load().Pomodoro
	if !p.Sound {
		return
	}
	text := "FOCUS"
	if res.Phase == pomodoro.PhaseFocus {
		text = "BREAK"
	}
	payload := map[string]any{
		"text": text, "wakeup": true, "durationMs": 4000, "stack": false,
		"name": notifyNamePomodoro,
	}
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
		a.rejectBody(w, r, err)
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
	a.pomoChanged()
	a.writePomoState(w)
}

func (a *App) handlePomodoroPause(w http.ResponseWriter, r *http.Request) {
	if !a.pomodoroOn() {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	a.engine.Pause(time.Now())
	a.pomoChanged()
	a.writePomoState(w)
}

func (a *App) handlePomodoroResume(w http.ResponseWriter, r *http.Request) {
	if !a.pomodoroOn() {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	a.engine.Resume(time.Now())
	a.pomoChanged()
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
	a.pomoChanged()
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
	a.pomoChanged()
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
	serveSettingGet(w, a.settings.pomodoro)
}

func (a *App) handlePomodoroConfigPut(w http.ResponseWriter, r *http.Request) {
	if d, ok := serveSettingPut(a, w, r, a.settings.pomodoro); ok {
		writeJSON(w, http.StatusOK, d)
	}
}

func (a *App) pomodoroSettingSpec() settingSpec[pomodoroSettingsDTO] {
	return settingSpec[pomodoroSettingsDTO]{
		key:  pomodoroSettingsKey,
		view: func(c Config) pomodoroSettingsDTO { return dtoFromConfig(c.Pomodoro) },
		apply: func(c *Config, d pomodoroSettingsDTO) error {
			p := c.Pomodoro
			d.mergeInto(&p)
			if err := validatePomodoro(p); err != nil {
				return err
			}
			c.Pomodoro = p
			return nil
		},
		after: func(c Config) {
			if a.engine != nil {
				a.engine.UpdateSettings(engineSettings(c.Pomodoro))
				if !c.Pomodoro.Enabled && a.engine.Status(time.Now()).Phase != pomodoro.PhaseIdle {
					a.engine.Stop(time.Now())
				}
			}
			a.pomoChanged()
			go a.ensureNativeIcons(context.Background())
		},
	}
}

func (d pomodoroSettingsDTO) mergeInto(p *PomodoroConfig) {
	setIf(&p.Enabled, d.Enabled)
	setIf(&p.FocusMinutes, d.FocusMinutes)
	setIf(&p.ShortBreakMinutes, d.ShortBreakMinutes)
	setIf(&p.LongBreakMinutes, d.LongBreakMinutes)
	setIf(&p.RoundsBeforeLongBreak, d.RoundsBeforeLongBreak)
	setIf(&p.AutoStartNext, d.AutoStartNext)
	setIf(&p.Sound, d.Sound)
	setIf(&p.SoundMelody, d.SoundMelody)
	setIf(&p.FocusColor, d.FocusColor)
	setIf(&p.BreakColor, d.BreakColor)
	setIf(&p.MaxSessionMinutes, d.MaxSessionMinutes)
	setIf(&p.DailyGoalSessions, d.DailyGoalSessions)
	setIf(&p.WeeklyGoalDays, d.WeeklyGoalDays)
}

func setIf[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

func (a *App) resyncPomodoroAfterReload() {
	if a.engine == nil {
		return
	}
	a.engine.UpdateSettings(engineSettings(a.cfg.Load().Pomodoro))
}

func (a *App) handleAwtrixButton(w http.ResponseWriter, r *http.Request) {
	a.lastButtonAt.Store(time.Now().Unix())
	if !a.pomodoroOn() || !a.cfg.Load().Pomodoro.ButtonCallback {
		w.WriteHeader(http.StatusOK)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, buttonHookMaxBody)
	button, down, err := parseButtonEvent(r)
	if err != nil {
		status := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, err)
		return
	}
	now := time.Now()

	if held := a.reminderHeldUntil.Load(); held != 0 && now.UnixNano() < held {
		if down && (button == "middle" || button == "select") {
			a.reminderHeldUntil.Store(0)
			if a.publisher != nil {
				err := a.publisher.DismissNotifyByName(r.Context(), notifyNameReminder)
				if err != nil && !isAPINotFound(err) {
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
		a.pomoChanged()
	}
	w.WriteHeader(http.StatusOK)
}

const buttonHookMaxBody = 1024

func parseButtonEvent(r *http.Request) (button string, down bool, err error) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "application/json" {
		var ev struct {
			Button string `json:"button"`
			State  bool   `json:"state"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, buttonHookMaxBody)).Decode(&ev); err != nil {
			return "", false, err
		}
		return ev.Button, ev.State, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", false, err
	}
	return r.PostFormValue("button"), r.PostFormValue("state") == "1", nil
}

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
