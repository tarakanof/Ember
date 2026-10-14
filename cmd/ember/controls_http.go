package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	controlStepTTL     = 2 * time.Second
	controlPressTTL    = 5 * time.Second
	controlsPerMac     = 8
	controlWaitersMax  = 8
	controlMacsMax     = 64
	controlListenGrace = commandListenGrace
)

var (
	errNoControlTarget    = errors.New("no Mac is set to run knob controls")
	errControlTargetDeaf  = errors.New("the Mac set to run knob controls is not listening")
	errControlsMasterOnly = errors.New("changing knob controls requires EMBER_TOKEN")
)

type controlCommand struct {
	ID     string      `json:"id"`
	Kind   controlKind `json:"kind"`
	Key    string      `json:"key,omitempty"`
	Name   string      `json:"name,omitempty"`
	Script string      `json:"script,omitempty"`
	Axis   string      `json:"axis,omitempty"`
	Delta  int         `json:"delta,omitempty"`
	AgeMS  int64       `json:"age_ms"`
	at     time.Time
}

func (c controlCommand) ttl() time.Duration {
	if c.Kind == controlVolume || c.Kind == controlScroll {
		return controlStepTTL
	}
	return controlPressTTL
}

func (c controlCommand) merges(next controlCommand) bool {
	return c.Kind == next.Kind && (c.Kind == controlVolume || c.Kind == controlScroll && c.Axis == next.Axis)
}

type controlsService struct {
	queue *controlQueue
	keys  controlKeys
	limit *callerLimiter
}

func newControlsService() *controlsService {
	return &controlsService{
		queue: newControlQueue(),
		limit: &callerLimiter{burst: controlBurst, perSec: controlPerSec},
	}
}

type controlQueue struct {
	clock    func() time.Time
	mu       sync.Mutex
	seq      uint64
	queued   map[string][]controlCommand
	polling  map[string]int
	lastPoll map[string]time.Time
	waiters  int
	wake     chan struct{}
}

func newControlQueue() *controlQueue {
	return &controlQueue{
		clock:    time.Now,
		queued:   make(map[string][]controlCommand),
		polling:  make(map[string]int),
		lastPoll: make(map[string]time.Time),
		wake:     make(chan struct{}),
	}
}

func (q *controlQueue) listening(mac string, now time.Time) bool {
	return q.polling[mac] > 0 || now.Sub(q.lastPoll[mac]) <= controlListenGrace
}

func (q *controlQueue) push(mac string, cmd controlCommand) (controlCommand, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock()
	if !q.listening(mac, now) {
		return controlCommand{}, errControlTargetDeaf
	}
	cmds := dropStaleControls(q.queued[mac], now)
	cmd.at = now
	if n := len(cmds); n > 0 && cmds[n-1].merges(cmd) {
		cmds[n-1].Delta = clampDelta(cmds[n-1].Delta + cmd.Delta)
		cmds[n-1].at = now
		cmd = cmds[n-1]
	} else {
		if len(cmds) >= controlsPerMac {
			cmds = cmds[1:]
		}
		q.seq++
		cmd.ID = "k" + strconv.FormatUint(q.seq, 10)
		cmds = append(cmds, cmd)
	}
	q.queued[mac] = cmds
	close(q.wake)
	q.wake = make(chan struct{})
	return cmd, nil
}

func dropStaleControls(cmds []controlCommand, now time.Time) []controlCommand {
	out := cmds[:0]
	for _, c := range cmds {
		if now.Sub(c.at) < c.ttl() {
			out = append(out, c)
		}
	}
	return out
}

func (q *controlQueue) take(ctx context.Context, mac string, wait time.Duration) ([]controlCommand, error) {
	q.mu.Lock()
	if wait > 0 && q.waiters >= controlWaitersMax {
		q.mu.Unlock()
		return nil, errTooManyWaiters
	}
	q.waiters++
	q.polling[mac]++
	q.mu.Unlock()
	defer q.leave(mac)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		q.mu.Lock()
		now := q.clock()
		cmds := dropStaleControls(q.queued[mac], now)
		delete(q.queued, mac)
		wake := q.wake
		q.mu.Unlock()
		if len(cmds) > 0 || wait <= 0 {
			for i := range cmds {
				cmds[i].AgeMS = now.Sub(cmds[i].at).Milliseconds()
			}
			return cmds, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil
		case <-timer.C:
			return nil, nil
		case <-wake:
		}
	}
}

func (q *controlQueue) leave(mac string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.waiters--
	if q.polling[mac]--; q.polling[mac] <= 0 {
		delete(q.polling, mac)
	}
	now := q.clock()
	q.lastPoll[mac] = now
	if len(q.lastPoll) > controlMacsMax {
		oldest := ""
		for m, at := range q.lastPoll {
			if q.polling[m] == 0 && (oldest == "" || at.Before(q.lastPoll[oldest])) {
				oldest = m
			}
		}
		delete(q.lastPoll, oldest)
	}
}

type controlsMac struct {
	Name      string `json:"name"`
	Listening bool   `json:"listening"`
	LastPoll  string `json:"last_poll,omitempty"`
}

func (q *controlQueue) macs() []controlsMac {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock()
	out := make([]controlsMac, 0, len(q.lastPoll)+len(q.polling))
	seen := make(map[string]bool, len(q.lastPoll))
	for m, at := range q.lastPoll {
		seen[m] = true
		out = append(out, controlsMac{Name: m, Listening: q.listening(m, now),
			LastPoll: at.UTC().Truncate(time.Second).Format(time.RFC3339)})
	}
	for m := range q.polling {
		if !seen[m] {
			out = append(out, controlsMac{Name: m, Listening: true})
		}
	}
	slices.SortFunc(out, func(a, b controlsMac) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

type controlsConfigAnswer struct {
	ControlsConfig
	Macs []controlsMac `json:"macs"`
}

func (a *App) controlsAnswer(c ControlsConfig) controlsConfigAnswer {
	return controlsConfigAnswer{ControlsConfig: c, Macs: a.controls.queue.macs()}
}

func (a *App) handleControlsConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.controlsAnswer(a.settings.controls.get()))
}

func (a *App) handleControlsConfigPut(w http.ResponseWriter, r *http.Request) {
	if _, isClient := r.Context().Value(clientCallerKey{}).(clientCaller); isClient {
		a.logger.InfoContext(r.Context(), "auth scope denied", "reason", "master_required", "path", r.URL.Path, "method", r.Method)
		writeError(w, http.StatusForbidden, errControlsMasterOnly)
		return
	}
	if d, ok := serveSettingPut(a, w, r, a.settings.controls); ok {
		a.logger.Info("knob controls changed", "target", d.Target, "push", d.Push.Kind, "double_push", d.DoublePush.Kind,
			"long_push", d.LongPush.Kind, "turn", d.Turn.Kind)
		writeJSON(w, http.StatusOK, a.controlsAnswer(d))
	}
}

type controlsActionAnswer struct {
	Status string      `json:"status"`
	Kind   controlKind `json:"kind,omitempty"`
	Target string      `json:"target,omitempty"`
}

func (a *App) handleControlsAction(w http.ResponseWriter, r *http.Request) {
	var req controlsActionRequest
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	caller := "owner"
	if id := deviceIDFrom(r.Context()); id != "" {
		caller = "device:" + id
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > maxControlKey {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Idempotency-Key is longer than %d bytes", maxControlKey))
		return
	}
	s := a.controls
	if !s.keys.claim(caller, key, time.Now()) {
		writeJSON(w, http.StatusOK, controlsActionAnswer{Status: "duplicate"})
		return
	}
	if !s.limit.allowNow(caller) {
		s.keys.release(caller, key)
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, errors.New("control requests too fast"))
		return
	}
	cfg := a.settings.controls.get()
	act := cfg.action(req.Gesture)
	if act.Kind == controlNone {
		s.keys.release(caller, key)
		writeJSON(w, http.StatusOK, controlsActionAnswer{Status: "unassigned"})
		return
	}
	if cfg.Target == "" {
		s.keys.release(caller, key)
		a.logger.Info("knob control rejected", "gesture", req.Gesture, "caller", caller, "err", errNoControlTarget)
		writeError(w, http.StatusServiceUnavailable, errNoControlTarget)
		return
	}
	cmd, err := s.queue.push(cfg.Target, resolveControl(act, req.Steps))
	if err != nil {
		s.keys.release(caller, key)
		a.logger.Info("knob control rejected", "gesture", req.Gesture, "target", cfg.Target, "caller", caller, "err", err)
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	a.logger.Debug("knob control queued", "gesture", req.Gesture, "kind", cmd.Kind, "id", cmd.ID,
		"target", cfg.Target, "caller", caller)
	writeJSON(w, http.StatusAccepted, controlsActionAnswer{Status: "queued", Kind: cmd.Kind, Target: cfg.Target})
}

type controlCommandsAnswer struct {
	Commands []controlCommand `json:"commands"`
}

func (a *App) handleControlsCommands(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mac := q.Get("mac")
	if mac == "" || !utf8.ValidString(mac) || utf8.RuneCountInString(mac) > maxControlTarget {
		writeError(w, http.StatusBadRequest, fmt.Errorf("mac is required (at most %d characters)", maxControlTarget))
		return
	}
	wait, err := parseViewWait(q.Get("wait"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	wait = min(wait, commandsWaitMax)
	if wait > 0 {
		rc := http.NewResponseController(w)
		dl := time.Now().Add(wait + knobViewWriteSlack)
		_ = rc.SetWriteDeadline(dl)
		_ = rc.SetReadDeadline(dl)
	}
	cmds, err := a.controls.queue.take(r.Context(), mac, wait)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, err)
		return
	}
	if cmds == nil {
		cmds = []controlCommand{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, controlCommandsAnswer{Commands: cmds})
}
