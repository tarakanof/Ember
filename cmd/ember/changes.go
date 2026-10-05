package main

import "sync"

// changeTopic names one source of state a pull client reads. Topics are bits,
// so a subscriber can ask which moved since its last look (a later SSE
// /v1/events, #269 phase 2, maps them to event names; the knob view long-poll
// ignores them and compares ETags).
type changeTopic uint32

const (
	topicSessions   changeTopic = 1 << iota // agent sessions: upsert, delete, clear, reap
	topicPomodoro                           // engine state: start, pause, resume, stop, skip, phase end, settings
	topicWeather                            // a new observation
	topicBrightness                         // the clock-lux filter moved
	topicDevices                            // knob registry (epoch, config, rotation) or live mode
	topicConfig                             // the effective Config was replaced
	topicNowPlaying                         // now-playing: track, state, seek, pictures
	topicCount      = iota
)

var changeTopicNames = [topicCount]string{"sessions", "pomodoro", "weather", "brightness", "devices", "config", "nowplaying"}

// String lists the topic names joined by "|", for logs and tests.
func (t changeTopic) String() string {
	s := ""
	for i, name := range changeTopicNames {
		if t&(1<<i) != 0 {
			if s != "" {
				s += "|"
			}
			s += name
		}
	}
	return s
}

// changeBroadcaster wakes every waiter at once when state moves. A waiter
// takes the current channel (subscribe) before it reads the state, then blocks
// on it: a notify between the read and the block has already closed that
// channel, so no wakeup is lost. notify closes the channel and installs a new
// one; it never blocks and holds only the broadcaster's own lock, so callers
// may hold theirs.
//
// The sequence number and per-topic marks let a stream subscriber (SSE) learn
// what changed since its last delivery without a queue per subscriber.
type changeBroadcaster struct {
	mu     sync.Mutex
	seq    uint64
	ch     chan struct{}
	last   [topicCount]uint64 // seq of each topic's latest notify
	done   chan struct{}
	closed bool
}

func newChangeBroadcaster() *changeBroadcaster {
	return &changeBroadcaster{ch: make(chan struct{}), done: make(chan struct{})}
}

// notify records that t moved and wakes all current waiters. Nil-safe.
func (b *changeBroadcaster) notify(t changeTopic) {
	if b == nil || t == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.seq++
	for i := range b.last {
		if t&(1<<i) != 0 {
			b.last[i] = b.seq
		}
	}
	close(b.ch)
	b.ch = make(chan struct{})
}

// subscribe returns the current sequence and the channel the next notify
// closes. Take it before reading state.
func (b *changeBroadcaster) subscribe() (uint64, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq, b.ch
}

// since reports the topics notified after seq.
func (b *changeBroadcaster) since(seq uint64) changeTopic {
	b.mu.Lock()
	defer b.mu.Unlock()
	var t changeTopic
	for i, s := range b.last {
		if s > seq {
			t |= 1 << i
		}
	}
	return t
}

// stopped is closed by close: waiters answer at once so a graceful shutdown
// does not wait out their timeouts.
func (b *changeBroadcaster) stopped() <-chan struct{} { return b.done }

// close ends every wait for good; later notifies are no-ops. Idempotent.
func (b *changeBroadcaster) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.done)
}
