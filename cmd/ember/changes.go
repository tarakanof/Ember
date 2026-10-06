package main

import "sync"

type changeTopic uint32

const (
	topicSessions changeTopic = 1 << iota
	topicPomodoro
	topicWeather
	topicBrightness
	topicDevices
	topicConfig
	topicNowPlaying
	topicCount = iota
)

var changeTopicNames = [topicCount]string{"sessions", "pomodoro", "weather", "brightness", "devices", "config", "nowplaying"}

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

// Subscribe before reading state: a notify in between has closed that channel, so no wakeup is lost.
type changeBroadcaster struct {
	mu     sync.Mutex
	seq    uint64
	ch     chan struct{}
	last   [topicCount]uint64
	done   chan struct{}
	closed bool
}

func newChangeBroadcaster() *changeBroadcaster {
	return &changeBroadcaster{ch: make(chan struct{}), done: make(chan struct{})}
}

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

func (b *changeBroadcaster) subscribe() (uint64, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq, b.ch
}

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

func (b *changeBroadcaster) stopped() <-chan struct{} { return b.done }

func (b *changeBroadcaster) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.done)
}
