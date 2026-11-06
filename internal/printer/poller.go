package printer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

const (
	PollActive     = 5 * time.Second
	PollIdle       = 30 * time.Second
	PollDisabled   = 5 * time.Minute
	offlineAfter   = 2
	thumbTries     = 3
	jobTries       = 3
	versionTries   = 3
	FallbackBrowse = 5 * time.Minute
	WatchRefresh   = 5 * time.Minute
	wakeRetries    = 2
)

var Backoff = []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second, 300 * time.Second}

type Found struct {
	Name string
	Host string
	URL  string
}

type BrowseFunc func(ctx context.Context) ([]Found, error)

type WatchFunc func(ctx context.Context, seen func(Found)) error

type Options struct {
	Config func() Config
	Browse BrowseFunc
	Watch  WatchFunc
	HTTP   *http.Client
	Logger *slog.Logger
	Now    func() time.Time
}

type Poller struct {
	State    *State
	OnChange func()

	cfg    func() Config
	browse BrowseFunc
	watch  WatchFunc
	hc     *http.Client
	logger *slog.Logger
	now    func() time.Time
	wake   chan struct{}

	credsMu sync.Mutex
	creds   Credentials

	client       *Client
	target       string
	identity     string
	manual       bool
	selName      string
	matched      bool
	failures     int
	backoff      int
	active       bool
	latched      string
	loggedErr    string
	needJob      bool
	versionTried bool
	versionFails int
	fallbackAt   time.Time
	browseErr    error
	browsed      bool
	jobFails     int
	thumbFails   int
	thumbFailRef string
	changeKey    string

	watchKey  string
	watchStop context.CancelFunc
	watchDone chan struct{}
	appeared  atomic.Bool
	woke      atomic.Bool
	retries   int
	spellKey  string
	watchAt   time.Time
}

func NewPoller(o Options) *Poller {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Config == nil {
		o.Config = func() Config { return Config{} }
	}
	return &Poller{
		State:  NewState(),
		cfg:    o.Config,
		browse: o.Browse,
		watch:  o.Watch,
		hc:     o.HTTP,
		logger: o.Logger,
		now:    o.Now,
		wake:   make(chan struct{}, 1),
	}
}

func (p *Poller) SetCredentials(c Credentials) {
	p.credsMu.Lock()
	p.creds = c
	p.credsMu.Unlock()
	p.Wake()
}

func (p *Poller) credentials() Credentials {
	p.credsMu.Lock()
	defer p.credsMu.Unlock()
	return p.creds
}

func (p *Poller) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Poller) Run(ctx context.Context) {
	select {
	case <-p.wake:
	default:
	}
	defer p.stopWatch()
	for {
		d := p.Step(ctx)
		if ctx.Err() != nil {
			return
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-p.wake:
			t.Stop()
		}
	}
}

func (p *Poller) Step(ctx context.Context) time.Duration {
	defer p.notifyIfChanged()
	p.browsed = false
	cfg := p.cfg()
	cfg.ApplyDefaults()
	defer p.syncWatch(ctx, cfg)
	if p.appeared.Swap(false) {
		p.backoff, p.retries = 0, wakeRetries
	}
	p.State.configure(cfg.Enabled, time.Duration(cfg.DoneHoldMin)*time.Minute, cfg.Name)
	if !cfg.Enabled {
		p.disconnect()
		p.identity = ""
		p.latched, p.loggedErr = "", ""
		p.State.forget()
		p.State.setOff(ErrCodeNone)
		p.stopWatch()
		p.woke.Store(false)
		p.retries = 0
		return PollDisabled
	}
	creds := p.credentials()
	if creds.Password == "" {
		p.disconnect()
		p.State.setOff(ErrCodeNoPassword)
		p.logOnce(ErrCodeNoPassword, errors.New("PrusaLink password unset ("+PasswordEnv+")"))
		return PollDisabled
	}
	key := latchKey(cfg, creds)
	if p.latched == key {
		if !cfg.Auto() || p.client == nil || p.matched || !p.fallbackDue() {
			return PollDisabled
		}
		prev := p.identity
		p.discover(ctx, cfg.Name, creds)
		if ctx.Err() != nil {
			return 0
		}
		if p.identity == prev {
			return PollDisabled
		}
	}
	p.latched = ""

	if !cfg.Auto() {
		if !p.manual || p.target != cfg.URL {
			p.use(Found{Host: hostOf(cfg.URL), URL: cfg.URL}, true, creds)
		}
	} else {
		if p.manual || (p.client != nil && p.selName != cfg.Name) {
			p.disconnect()
		}
		if p.client != nil && !p.matched && p.fallbackDue() {
			p.discover(ctx, cfg.Name, creds)
			if ctx.Err() != nil {
				return 0
			}
		}
		if p.client == nil {
			if !p.discover(ctx, cfg.Name, creds) {
				if ctx.Err() != nil {
					return 0
				}
				p.State.setTarget("", "", false)
				p.State.setOffline(ErrCodeNotFound)
				p.logOnce(ErrCodeNotFound, cmpErr(p.browseErr, errors.New("no _prusalink._tcp service on the network")))
				return p.offlineDelay()
			}
		}
	}
	if p.client != nil && p.client.creds != creds {
		p.use(Found{Host: p.State.At(p.now()).Printer.Host, URL: p.target}, p.manual, creds)
	}
	if p.client == nil {
		p.State.setOffline(ErrCodeUnreachable)
		return p.offlineDelay()
	}

	err := p.poll(ctx)
	if err != nil && ctx.Err() != nil {
		return 0
	}
	if err != nil && !errors.Is(err, ErrWrongPassword) && cfg.Auto() && p.failures+1 >= offlineAfter && !p.browsed {
		prev := p.target
		if p.discover(ctx, cfg.Name, creds) && p.target != prev {
			p.failures = 0
			err = p.poll(ctx)
		}
	}
	switch {
	case err == nil:
		if p.failures >= offlineAfter {
			p.versionTried, p.versionFails = false, 0
		}
		p.failures, p.backoff, p.retries = 0, 0, 0
		p.woke.Store(false)
		if p.loggedErr != "" {
			p.logger.Info("printer reachable", "url", p.target)
			p.loggedErr = ""
		}
		if p.active {
			return PollActive
		}
		return PollIdle
	case errors.Is(err, ErrWrongPassword):
		p.latched = key
		p.State.setOffline(ErrCodeWrongPassword)
		p.logOnce(ErrCodeWrongPassword, err)
		return PollDisabled
	case ctx.Err() != nil:
		return 0
	}
	p.failures++
	p.logOnce(ErrCodeUnreachable, err)
	if p.failures < offlineAfter {
		if p.retries > 0 {
			p.retries--
			return PollActive
		}
		if p.active {
			return PollActive
		}
		return PollIdle
	}
	p.State.setOffline(ErrCodeUnreachable)
	return p.offlineDelay()
}

func (p *Poller) syncWatch(ctx context.Context, cfg Config) {
	s := p.State.At(p.now())
	want := p.watch != nil && ctx.Err() == nil && cfg.Enabled && s.Phase == PhaseOffline &&
		(s.LastError == ErrCodeUnreachable || s.LastError == ErrCodeNotFound)
	if !want {
		p.stopWatch()
		return
	}
	identity, named := p.identity, p.matched
	if p.client == nil {
		identity, named = "", false
	}
	match := watchMatcher(cfg, identity, named)
	key := fmt.Sprint(cfg.URL, "\x00", cfg.Name, "\x00", identity, "\x00", named)
	if spell := latchKey(cfg, p.credentials()); spell != p.spellKey {
		p.stopWatch()
		p.spellKey = spell
		p.woke.Store(false)
		p.retries = 0
	}
	now := p.now()
	if p.watchStop != nil && p.watchKey == key && now.Sub(p.watchAt) < WatchRefresh {
		select {
		case <-p.watchDone:
		default:
			return
		}
	}
	p.stopWatch()
	p.watchAt = now
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	p.watchKey, p.watchStop, p.watchDone = key, cancel, done
	watch, logger := p.watch, p.logger
	go func() {
		defer close(done)
		err := watch(wctx, func(f Found) {
			if wctx.Err() == nil && match(f) && p.woke.CompareAndSwap(false, true) {
				p.appeared.Store(true)
				p.Wake()
			}
		})
		if err != nil && wctx.Err() == nil {
			logger.Debug("printer mDNS watch ended", "err", err)
		}
	}()
}

func (p *Poller) stopWatch() {
	if p.watchStop == nil {
		return
	}
	p.watchStop()
	p.watchKey, p.watchStop, p.watchDone = "", nil, nil
}

func watchMatcher(cfg Config, identity string, named bool) func(Found) bool {
	if !cfg.Auto() {
		want := strings.ToLower(hostnameOf(cfg.URL))
		named := strings.IndexFunc(want, unicode.IsLetter) >= 0 && !strings.Contains(want, ":")
		return func(f Found) bool {
			if want == "" {
				return false
			}
			if strings.ToLower(hostnameOf(f.URL)) == want {
				return true
			}
			return named && firstLabel(strings.ToLower(f.Host)) == firstLabel(want)
		}
	}
	name := cfg.Name
	return func(f Found) bool {
		if identity != "" && strings.TrimSuffix(f.Host, ".") == identity {
			return true
		}
		if identity != "" && named {
			return false
		}
		if _, matched := Choose([]Found{f}, name); matched {
			return true
		}
		return identity == ""
	}
}

func latchKey(cfg Config, c Credentials) string {
	sel := cfg.URL
	if cfg.Auto() {
		sel += "\x00" + cfg.Name
	}
	sum := sha256.Sum256([]byte(sel + "\x00" + c.User + "\x00" + c.Password))
	return string(sum[:])
}

func (p *Poller) fallbackDue() bool {
	return !p.now().Before(p.fallbackAt)
}

func (p *Poller) offlineDelay() time.Duration {
	if p.retries > 0 {
		p.retries--
		return PollActive
	}
	return p.nextBackoff()
}

func (p *Poller) nextBackoff() time.Duration {
	d := Backoff[min(p.backoff, len(Backoff)-1)]
	p.backoff++
	return d
}

func (p *Poller) logOnce(code ErrorCode, err error) {
	if p.loggedErr == string(code) {
		return
	}
	p.loggedErr = string(code)
	p.logger.Warn("printer poll failed", "reason", string(code), "url", p.target, "err", err)
}

func (p *Poller) disconnect() {
	p.client, p.target, p.manual = nil, "", false
	p.failures, p.versionTried, p.versionFails = 0, false, 0
}

func (p *Poller) use(f Found, manual bool, creds Credentials) {
	host := strings.TrimSuffix(f.Host, ".")
	identity := host
	if manual {
		identity = f.URL
	}
	if p.client != nil && f.URL == p.target && manual == p.manual && identity == p.identity && p.client.creds == creds {
		return
	}
	c, err := NewClient(f.URL, creds, p.hc)
	if err != nil {
		p.disconnect()
		return
	}
	if p.identity != "" && identity != p.identity {
		p.State.forget()
		p.thumbFails, p.thumbFailRef = 0, ""
		p.versionTried, p.versionFails = false, 0
	}
	if f.URL != p.target {
		p.versionTried, p.versionFails = false, 0
		p.failures, p.backoff = 0, 0
		p.needJob, p.jobFails = true, 0
	}
	p.client, p.target, p.manual, p.identity = c, f.URL, manual, identity
	p.State.setTarget(f.URL, host, !manual)
}

func (p *Poller) discover(ctx context.Context, name string, creds Credentials) bool {
	if p.browse == nil {
		return false
	}
	if p.client != nil && !p.matched {
		p.fallbackAt = p.now().Add(FallbackBrowse)
	}
	found, err := p.browse(ctx)
	p.browsed, p.browseErr = true, err
	if err != nil || len(found) == 0 {
		return false
	}
	f, matched := Choose(found, name)
	if p.client != nil && (p.matched || !matched) {
		same := slices.IndexFunc(found, func(c Found) bool { return strings.TrimSuffix(c.Host, ".") == p.identity })
		if same < 0 {
			return false
		}
		f, matched = found[same], p.matched
	}
	if f.URL != p.target {
		p.logger.Info("printer discovered", "host", f.Host, "url", f.URL, "candidates", len(found), "name_match", matched)
	}
	p.use(f, false, creds)
	p.selName, p.matched = name, matched
	if !matched {
		p.fallbackAt = p.now().Add(FallbackBrowse)
	}
	return p.client != nil
}

func Choose(found []Found, name string) (Found, bool) {
	sorted := slices.Clone(found)
	slices.SortFunc(sorted, func(a, b Found) int { return strings.Compare(a.Host+a.URL, b.Host+b.URL) })
	want := squash(name)
	if want == "" {
		return sorted[0], false
	}
	for _, f := range sorted {
		if squash(hostLabel(f.Host)) == want || squash(f.Name) == want {
			return f, true
		}
	}
	best, bestLen := -1, 0
	for i, f := range sorted {
		for _, label := range []string{squash(hostLabel(f.Host)), squash(f.Name)} {
			if strings.Contains(label, want) && (best < 0 || len(label) < bestLen) {
				best, bestLen = i, len(label)
			}
		}
	}
	if best >= 0 {
		return sorted[best], true
	}
	return sorted[0], false
}

func hostLabel(h string) string {
	h = strings.TrimSuffix(h, ".")
	return strings.TrimSuffix(h, ".local")
}

func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstLabel(h string) string {
	label, _, _ := strings.Cut(h, ".")
	return label
}

func hostnameOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(u.Hostname(), ".")
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func (p *Poller) poll(ctx context.Context) error {
	if p.client == nil {
		return errors.New("prusalink: no printer address")
	}
	st, err := p.client.Status(ctx)
	if err != nil {
		return err
	}
	now := p.now()
	stateChanged, jobChanged, active := p.State.observe(now, st)
	p.active = active
	if !p.versionTried {
		v, err := p.client.Version(ctx)
		if errors.Is(err, ErrWrongPassword) {
			return err
		}
		if err == nil {
			p.State.setVersion(v)
			p.versionTried = true
		} else if p.versionFails++; p.versionFails >= versionTries {
			p.versionTried = true
		}
	}
	if stateChanged || jobChanged {
		p.jobFails = 0
	}
	if stateChanged || jobChanged || p.needJob {
		if err := p.fetchJob(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (p *Poller) fetchJob(ctx context.Context) error {
	job, err := p.client.Job(ctx)
	if errors.Is(err, ErrWrongPassword) {
		return err
	}
	if err != nil {
		p.jobFails++
		p.needJob = p.jobFails < jobTries
		if !p.needJob {
			p.logger.Warn("printer job read failed", "err", err, "tries", p.jobFails)
		}
		return nil
	}
	p.needJob, p.jobFails = false, 0
	ref, fetch := p.State.setJob(job)
	if !fetch {
		return nil
	}
	b, err := p.client.Thumbnail(ctx, ref)
	switch {
	case err == nil:
		p.thumbFails = 0
		p.State.setThumb(ref, b)
	case errors.Is(err, ErrWrongPassword):
		return err
	case errors.Is(err, ErrNoThumbnail):
		p.thumbFails = 0
		p.State.setThumb(ref, nil)
	case errors.Is(err, ErrBadThumbnail):
		p.thumbFails = 0
		p.State.setThumb(ref, nil)
		p.logger.Info("printer thumbnail skipped", "err", err)
	default:
		if key := strconv.Itoa(job.ID) + "\x00" + ref; p.thumbFailRef != key {
			p.thumbFails, p.thumbFailRef = 0, key
		}
		p.thumbFails++
		if p.thumbFails < thumbTries {
			p.State.forgetThumbRef(ref)
			p.needJob = true
		} else {
			p.thumbFails = 0
			p.State.setThumb(ref, nil)
			p.logger.Info("printer thumbnail skipped", "err", err)
		}
	}
	return nil
}

func (p *Poller) notifyIfChanged() {
	s := p.State.At(p.now())
	evID := uint64(0)
	if s.Event != nil {
		evID = s.Event.ID
	}
	key := fmt.Sprint(s.Enabled, s.Phase, s.Progress, s.EndsAt.Unix(), s.StartedAt.Unix(), s.ChangeAt.Unix(),
		s.Name, s.Material, s.ThumbVersion, evID, s.HoldUntil.Unix(), s.LastError, s.JobID, s.Printer)
	if s.Phase == PhaseHeating {
		key += fmt.Sprint(int(s.Nozzle), int(s.NozzleTarget), int(s.Bed), int(s.BedTarget))
	}
	if p.cfg().ShowWhenOn() {
		key += "|on"
		if s.Phase == PhaseIdle && s.Warm() {
			nozzle, deg := s.ReadyTemp()
			key += fmt.Sprint("|warm", nozzle, deg)
		}
	}
	if key == p.changeKey {
		return
	}
	p.changeKey = key
	if p.OnChange != nil {
		p.OnChange()
	}
}

func cmpErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
