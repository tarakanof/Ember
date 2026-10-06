package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/nowplaying"
)

type controlAction string

const (
	actPlayPause controlAction = "play_pause"
	actPlay      controlAction = "play"
	actPause     controlAction = "pause"
	actNext      controlAction = "next"
	actPrevious  controlAction = "previous"
	actVolume    controlAction = "volume"
)

const (
	controlKeyTTL        = 2 * time.Minute
	controlKeysPerCaller = 128
	controlCallersMax    = 32
	maxControlKey        = 128
	commandTTL           = 5 * time.Second
	commandListenGrace   = 30 * time.Second
	commandsPerPlayer    = 8
	commandsWaitMax      = 25 * time.Second
	commandWaitersMax    = 8
	controlBurst         = 20
	controlPerSec        = 10.0
	plexCommandedTTL     = 5 * time.Second
)

var (
	errNothingPlaying = errors.New("nothing is playing")
	errNoController   = errors.New("the source of the shown track can't be controlled")
	errControlFailed  = errors.New("the player did not take the command")
	errShownChanged   = errors.New("the shown track changed")
)

type controlRequest struct {
	Action controlAction `json:"action"`
	// points, -100..100; volume only.
	Delta   int    `json:"delta"`
	Source  string `json:"source"`
	Player  string `json:"player"`
	TrackID string `json:"track_id"`
}

func (c controlRequest) matches(e nowplaying.Entry) bool {
	return (c.Source == "" || c.Source == e.Source) && (c.Player == "" || c.Player == e.Player) &&
		(c.TrackID == "" || c.TrackID == e.TrackID)
}

func (c controlRequest) validate() error {
	switch c.Action {
	case actPlayPause, actPlay, actPause, actNext, actPrevious:
		if c.Delta != 0 {
			return errors.New("delta is for volume only")
		}
	case actVolume:
		if c.Delta == 0 || c.Delta < -100 || c.Delta > 100 {
			return errors.New("volume needs a delta of -100..100, not 0")
		}
	default:
		return errors.New("action must be play_pause, play, pause, next, previous or volume")
	}
	return nil
}

type controlResult struct {
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
	Volume *int   `json:"volume,omitempty"`
}

func (s *nowPlayingService) control(ctx context.Context, e nowplaying.Entry, req controlRequest) (controlResult, error) {
	switch {
	case e.Source == plexSourceID && s.plex != nil:
		return s.plex.control(ctx, e, req)
	case e.Source == musicSourceID:
		if err := s.commands.push(e.Player, req); err != nil {
			return controlResult{}, err
		}
		return controlResult{Status: "queued", Source: e.Source}, nil
	}
	return controlResult{}, errNoController
}

const musicSourceID = "music"

func (a *App) handleNowPlayingControl(w http.ResponseWriter, r *http.Request) {
	var req controlRequest
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
		cfg, _, err := a.devices.config(id)
		if err != nil {
			a.writeDeviceError(w, r, err)
			return
		}
		if !cfg.pageOn(knobNowPlayingPage) {
			writeError(w, http.StatusForbidden, errors.New("the device's now-playing page is off"))
			return
		}
	}
	now := time.Now()
	key := r.Header.Get("Idempotency-Key")
	if len(key) > maxControlKey {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Idempotency-Key is longer than %d bytes", maxControlKey))
		return
	}
	if !a.nowPlaying.controlKeys.claim(caller, key, now) {
		a.logger.Info("now-playing control duplicate", "action", req.Action, "caller", caller)
		writeJSON(w, http.StatusOK, controlResult{Status: "duplicate"})
		return
	}
	if !a.nowPlaying.controlLimit.allowNow(caller) {
		a.nowPlaying.controlKeys.release(caller, key)
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, errors.New("control requests too fast"))
		return
	}
	e, ok := a.nowPlaying.reg.Current(now)
	if !ok || !req.matches(e) {
		a.nowPlaying.controlKeys.release(caller, key)
		err := errNothingPlaying
		if ok {
			err = errShownChanged
		}
		writeError(w, http.StatusConflict, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*plexTimeout)
	defer cancel()
	res, err := a.nowPlaying.control(ctx, e, req)
	a.logger.Info("now-playing control", "action", req.Action, "delta", req.Delta, "source", e.Source,
		"caller", caller, "status", res.Status, "err", err)
	switch {
	case errors.Is(err, errNoController):
		a.nowPlaying.controlKeys.release(caller, key)
		writeError(w, http.StatusServiceUnavailable, err)
	case err != nil:
		// The key stays claimed: the player may have taken it, so a retry must not skip twice.
		writeError(w, http.StatusBadGateway, err)
	case res.Status == "queued":
		writeJSON(w, http.StatusAccepted, res)
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

type controlKeys struct {
	mu      sync.Mutex
	callers map[string]map[string]time.Time
}

func (k *controlKeys) claim(caller, key string, now time.Time) bool {
	if key == "" {
		return true
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.callers == nil {
		k.callers = make(map[string]map[string]time.Time)
	}
	seen := k.callers[caller]
	if seen == nil {
		if len(k.callers) >= controlCallersMax {
			for c, m := range k.callers {
				if keysExpired(m, now) {
					delete(k.callers, c)
				}
			}
			if len(k.callers) >= controlCallersMax {
				clear(k.callers)
			}
		}
		seen = make(map[string]time.Time)
		k.callers[caller] = seen
	}
	var oldest string
	for s, at := range seen {
		if now.Sub(at) >= controlKeyTTL {
			delete(seen, s)
		} else if oldest == "" || at.Before(seen[oldest]) {
			oldest = s
		}
	}
	if _, ok := seen[key]; ok {
		return false
	}
	if len(seen) >= controlKeysPerCaller {
		delete(seen, oldest)
	}
	seen[key] = now
	return true
}

func keysExpired(m map[string]time.Time, now time.Time) bool {
	for _, at := range m {
		if now.Sub(at) < controlKeyTTL {
			return false
		}
	}
	return true
}

func (k *controlKeys) release(caller, key string) {
	if key == "" {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.callers[caller], key)
}

type callerLimiter struct {
	burst, perSec float64
	clock         func() time.Time
	mu            sync.Mutex
	buckets       map[string]*callerBucket
}

type callerBucket struct {
	tokens float64
	at     time.Time
}

func (l *callerLimiter) allowNow(caller string) bool {
	now := time.Now()
	if l.clock != nil {
		now = l.clock()
	}
	return l.allow(caller, now)
}

func (l *callerLimiter) allow(caller string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = make(map[string]*callerBucket)
	}
	b := l.buckets[caller]
	if b == nil {
		if len(l.buckets) >= 64 {
			clear(l.buckets)
		}
		b = &callerBucket{tokens: l.burst, at: now}
		l.buckets[caller] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.at).Seconds()*l.perSec)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type queuedCommand struct {
	ID     string        `json:"id"`
	Action controlAction `json:"action"`
	Delta  int           `json:"delta"`
	AgeMS  int64         `json:"age_ms"`
	at     time.Time
}

type commandQueue struct {
	clock    func() time.Time
	mu       sync.Mutex
	seq      uint64
	queued   map[string][]queuedCommand
	polling  map[string]int
	lastPoll map[string]time.Time
	waiters  int
	wake     chan struct{}
}

func newCommandQueue() *commandQueue {
	return &commandQueue{
		clock:    time.Now,
		queued:   make(map[string][]queuedCommand),
		polling:  make(map[string]int),
		lastPoll: make(map[string]time.Time),
		wake:     make(chan struct{}),
	}
}

func (q *commandQueue) push(player string, req controlRequest) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock()
	if q.polling[player] == 0 && now.Sub(q.lastPoll[player]) > commandListenGrace {
		return errNoController
	}
	cmds := dropStale(q.queued[player], now)
	if n := len(cmds); req.Action == actVolume && n > 0 && cmds[n-1].Action == actVolume {
		cmds[n-1].Delta = max(-100, min(100, cmds[n-1].Delta+req.Delta))
		cmds[n-1].at = now
	} else {
		if len(cmds) >= commandsPerPlayer {
			cmds = cmds[1:]
		}
		q.seq++
		cmds = append(cmds, queuedCommand{ID: "c" + strconv.FormatUint(q.seq, 10), Action: req.Action, Delta: req.Delta, at: now})
	}
	q.queued[player] = cmds
	close(q.wake)
	q.wake = make(chan struct{})
	return nil
}

func dropStale(cmds []queuedCommand, now time.Time) []queuedCommand {
	out := cmds[:0]
	for _, c := range cmds {
		if now.Sub(c.at) < commandTTL {
			out = append(out, c)
		}
	}
	return out
}

var errTooManyWaiters = errors.New("too many waiting command polls")

func (q *commandQueue) take(ctx context.Context, player string, wait time.Duration) ([]queuedCommand, error) {
	q.mu.Lock()
	if wait > 0 && q.waiters >= commandWaitersMax {
		q.mu.Unlock()
		return nil, errTooManyWaiters
	}
	q.waiters++
	q.polling[player]++
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		q.waiters--
		if q.polling[player]--; q.polling[player] <= 0 {
			delete(q.polling, player)
		}
		q.lastPoll[player] = q.clock()
		if len(q.lastPoll) > 64 {
			for p, at := range q.lastPoll {
				if q.clock().Sub(at) > commandListenGrace {
					delete(q.lastPoll, p)
				}
			}
		}
		q.mu.Unlock()
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		q.mu.Lock()
		cmds := dropStale(q.queued[player], q.clock())
		delete(q.queued, player)
		wake := q.wake
		q.mu.Unlock()
		if len(cmds) > 0 || wait <= 0 {
			for i := range cmds {
				cmds[i].AgeMS = q.clock().Sub(cmds[i].at).Milliseconds()
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

type commandsAnswer struct {
	Commands []queuedCommand `json:"commands"`
}

func (a *App) handleNowPlayingCommands(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	player := q.Get("player")
	if player == "" || !utf8.ValidString(player) || utf8.RuneCountInString(player) > 64 {
		writeError(w, http.StatusBadRequest, errors.New("player is required (at most 64 characters)"))
		return
	}
	wait, err := parseViewWait(q.Get("wait"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if wait > commandsWaitMax {
		wait = commandsWaitMax
	}
	if wait > 0 {
		rc := http.NewResponseController(w)
		dl := time.Now().Add(wait + knobViewWriteSlack)
		_ = rc.SetWriteDeadline(dl)
		_ = rc.SetReadDeadline(dl)
	}
	cmds, err := a.nowPlaying.commands.take(r.Context(), player, wait)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, err)
		return
	}
	if cmds == nil {
		cmds = []queuedCommand{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, commandsAnswer{Commands: cmds})
}
