package nowplaying

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

type ControlAction string

const (
	actPlayPause ControlAction = "play_pause"
	actPlay      ControlAction = "play"
	actPause     ControlAction = "pause"
	ActNext      ControlAction = "next"
	actPrevious  ControlAction = "previous"
	ActVolume    ControlAction = "volume"
)

const (
	controlKeyTTL        = 2 * time.Minute
	controlKeysPerCaller = 128
	controlCallersMax    = 32
	commandTTL           = 5 * time.Second
	CommandListenGrace   = 30 * time.Second
	commandsPerPlayer    = 8
	commandWaitersMax    = 8
	ControlBurst         = 20
	ControlPerSec        = 10.0
	plexCommandedTTL     = 5 * time.Second
)

var (
	ErrNoController  = errors.New("the source of the shown track can't be controlled")
	errControlFailed = errors.New("the player did not take the command")
)

type ControlRequest struct {
	Action ControlAction `json:"action"`
	// points, -100..100; volume only.
	Delta   int    `json:"delta"`
	Source  string `json:"source"`
	Player  string `json:"player"`
	TrackID string `json:"track_id"`
}

func (c ControlRequest) Matches(e Entry) bool {
	return (c.Source == "" || c.Source == e.Source) && (c.Player == "" || c.Player == e.Player) &&
		(c.TrackID == "" || c.TrackID == e.TrackID)
}

func (c ControlRequest) Validate() error {
	switch c.Action {
	case actPlayPause, actPlay, actPause, ActNext, actPrevious:
		if c.Delta != 0 {
			return errors.New("delta is for volume only")
		}
	case ActVolume:
		if c.Delta == 0 || c.Delta < -100 || c.Delta > 100 {
			return errors.New("volume needs a delta of -100..100, not 0")
		}
	default:
		return errors.New("action must be play_pause, play, pause, next, previous or volume")
	}
	return nil
}

type ControlResult struct {
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
	Volume *int   `json:"volume,omitempty"`
}

func (s *Service) Control(ctx context.Context, e Entry, req ControlRequest) (ControlResult, error) {
	switch {
	case e.Source == plexSourceID && s.Plex != nil:
		return s.Plex.control(ctx, e, req)
	case e.Source == musicSourceID:
		if err := s.Commands.Push(e.Player, req); err != nil {
			return ControlResult{}, err
		}
		return ControlResult{Status: "queued", Source: e.Source}, nil
	}
	return ControlResult{}, ErrNoController
}

const musicSourceID = "music"

type ControlKeys struct {
	mu      sync.Mutex
	callers map[string]map[string]time.Time
}

func (k *ControlKeys) Claim(caller, key string, now time.Time) bool {
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

func (k *ControlKeys) Release(caller, key string) {
	if key == "" {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.callers[caller], key)
}

type CallerLimiter struct {
	Burst, PerSec float64
	Clock         func() time.Time
	mu            sync.Mutex
	buckets       map[string]*callerBucket
}

type callerBucket struct {
	tokens float64
	at     time.Time
}

func (l *CallerLimiter) AllowNow(caller string) bool {
	now := time.Now()
	if l.Clock != nil {
		now = l.Clock()
	}
	return l.allow(caller, now)
}

func (l *CallerLimiter) allow(caller string, now time.Time) bool {
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
		b = &callerBucket{tokens: l.Burst, at: now}
		l.buckets[caller] = b
	}
	b.tokens = min(l.Burst, b.tokens+now.Sub(b.at).Seconds()*l.PerSec)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type QueuedCommand struct {
	ID     string        `json:"id"`
	Action ControlAction `json:"action"`
	Delta  int           `json:"delta"`
	AgeMS  int64         `json:"age_ms"`
	at     time.Time
}

type CommandQueue struct {
	clock    func() time.Time
	mu       sync.Mutex
	seq      uint64
	queued   map[string][]QueuedCommand
	polling  map[string]int
	lastPoll map[string]time.Time
	waiters  int
	wake     chan struct{}
}

func newCommandQueue() *CommandQueue {
	return &CommandQueue{
		clock:    time.Now,
		queued:   make(map[string][]QueuedCommand),
		polling:  make(map[string]int),
		lastPoll: make(map[string]time.Time),
		wake:     make(chan struct{}),
	}
}

func (q *CommandQueue) Pollers(player string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.polling[player]
}

func (q *CommandQueue) WaiterCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.waiters
}

func (q *CommandQueue) Push(player string, req ControlRequest) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock()
	if q.polling[player] == 0 && now.Sub(q.lastPoll[player]) > CommandListenGrace {
		return ErrNoController
	}
	cmds := dropStale(q.queued[player], now)
	if n := len(cmds); req.Action == ActVolume && n > 0 && cmds[n-1].Action == ActVolume {
		cmds[n-1].Delta = max(-100, min(100, cmds[n-1].Delta+req.Delta))
		cmds[n-1].at = now
	} else {
		if len(cmds) >= commandsPerPlayer {
			cmds = cmds[1:]
		}
		q.seq++
		cmds = append(cmds, QueuedCommand{ID: "c" + strconv.FormatUint(q.seq, 10), Action: req.Action, Delta: req.Delta, at: now})
	}
	q.queued[player] = cmds
	close(q.wake)
	q.wake = make(chan struct{})
	return nil
}

func dropStale(cmds []QueuedCommand, now time.Time) []QueuedCommand {
	out := cmds[:0]
	for _, c := range cmds {
		if now.Sub(c.at) < commandTTL {
			out = append(out, c)
		}
	}
	return out
}

var ErrTooManyWaiters = errors.New("too many waiting command polls")

type TakeUntil func() (wake <-chan struct{}, recheck <-chan time.Time, stop func(), done bool)

func (q *CommandQueue) Take(ctx context.Context, player string, wait time.Duration, until TakeUntil) ([]QueuedCommand, error) {
	q.mu.Lock()
	if wait > 0 && q.waiters >= commandWaitersMax {
		q.mu.Unlock()
		return nil, ErrTooManyWaiters
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
				if q.clock().Sub(at) > CommandListenGrace {
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
		var other <-chan struct{}
		var recheck <-chan time.Time
		stop := func() {}
		if until != nil {
			var done bool
			if other, recheck, stop, done = until(); done {
				return nil, nil
			}
		}
		select {
		case <-ctx.Done():
			stop()
			return nil, nil
		case <-timer.C:
			stop()
			return nil, nil
		case <-wake:
		case <-other:
		case <-recheck:
		}
		stop()
	}
}
