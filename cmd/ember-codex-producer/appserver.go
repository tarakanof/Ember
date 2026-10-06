package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

// Name is on the server's NON_ORIGINATING_CLIENT_NAMES list, so Ember never relabels the user's Codex sessions; see ARCHITECTURE.
const appServerClientName = "codex_app_server_daemon"

var appServerOptOut = []string{
	"item/agentMessage/delta", "item/plan/delta", "item/reasoning/summaryTextDelta",
	"item/reasoning/summaryPartAdded", "item/reasoning/textDelta",
	"item/commandExecution/outputDelta", "item/fileChange/outputDelta",
	"command/exec/outputDelta", "process/outputDelta", "turn/diff/updated",
	"item/mcpToolCall/progress", "fuzzyFileSearch/sessionUpdated",
}

func appServerSocket(codexHome string) string {
	return filepath.Join(codexHome, "app-server-control", "app-server-control.sock")
}

type apThread struct {
	id         string
	tracked    bool
	viaClaude  bool
	d          derived
	busy       bool
	subscribed bool
	resumeAt   time.Time
	failed     bool
	lastChange time.Time
	post       producer.Repost
}

type apPending struct {
	inflight bool
	status   *wireStatus
	closed   bool
}

const ephemeralTTL = 10 * time.Minute

type appServer struct {
	cfg         Config
	sock        string
	now         func() time.Time
	pollEvery   time.Duration
	backoffMin  time.Duration
	backoffMax  time.Duration
	retryEvery  time.Duration
	callTimeout time.Duration
	liveEvery   time.Duration
	liveTimeout time.Duration
	failLog     *producer.FailureLogger

	kick chan struct{}

	mu           sync.Mutex
	connected    bool
	userAgent    string
	threads      map[string]*apThread
	unread       map[string]*apPending
	ephemeral    map[string]time.Time
	gone         map[string]time.Time
	lastReleased map[string]bool
	deletes      []string
	released     []string
	rate         derived
	hasRate      bool
}

func newAppServer(cfg Config) *appServer {
	return &appServer{
		cfg:         cfg,
		sock:        cfg.AppServerSocket,
		now:         time.Now,
		pollEvery:   2 * time.Second,
		backoffMin:  time.Second,
		backoffMax:  30 * time.Second,
		retryEvery:  time.Second,
		callTimeout: 30 * time.Second,
		liveEvery:   time.Minute,
		liveTimeout: 10 * time.Second,
		failLog:     producer.NewFailureLogger(10 * time.Minute),
		kick:        make(chan struct{}, 1),
		threads:     map[string]*apThread{},
		unread:      map[string]*apPending{},
		ephemeral:   map[string]time.Time{},
		gone:        map[string]time.Time{},
	}
}

func (as *appServer) wake() {
	select {
	case as.kick <- struct{}{}:
	default:
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (as *appServer) run(ctx context.Context) {
	backoff := as.backoffMin
	for ctx.Err() == nil {
		if _, err := os.Stat(as.sock); err != nil {
			backoff = as.backoffMin
			if !sleepCtx(ctx, as.pollEvery) {
				return
			}
			continue
		}
		start := time.Now()
		err := as.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) >= as.backoffMax {
			backoff = as.backoffMin
		}
		if err != nil {
			as.failLog.Warn(slog.Default(), "codex_appserver", "codex app-server connection failed", "err", err, "retry_in", backoff)
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, as.backoffMax)
	}
}

func (as *appServer) session(ctx context.Context) error {
	ws, err := dialWS(ctx, as.sock)
	if err != nil {
		return err
	}
	sctx, cancel := context.WithCancel(ctx)
	c := newRPCConn(ws)
	go c.readLoop(as.onNotification)
	defer func() {
		cancel()
		_ = ws.Close()
		<-c.done
		as.disconnect()
	}()
	go func() {
		select {
		case <-sctx.Done():
			_ = ws.Close()
		case <-c.done:
		}
	}()

	var init struct {
		UserAgent string `json:"userAgent"`
	}
	if err := as.call(sctx, c, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": appServerClientName, "title": "Ember (passive observer)", "version": version},
		"capabilities": map[string]any{"optOutNotificationMethods": appServerOptOut},
	}, &init); err != nil {
		return err
	}
	if err := c.notify("initialized"); err != nil {
		return err
	}
	as.mu.Lock()
	as.connected, as.userAgent = true, init.UserAgent
	as.mu.Unlock()
	slog.Info("codex app-server connected", "socket", as.sock, "user_agent", init.UserAgent)

	loaded := map[string]bool{}
	cursor := ""
	for page := 0; page < 50; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var ll struct {
			Data       []string `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		if err := as.call(sctx, c, "thread/loaded/list", params, &ll); err != nil {
			return err
		}
		as.mu.Lock()
		for _, id := range ll.Data {
			loaded[id] = true
			if as.threads[id] == nil && as.unread[id] == nil {
				as.unread[id] = &apPending{}
			}
		}
		as.mu.Unlock()
		if ll.NextCursor == nil || *ll.NextCursor == "" {
			break
		}
		cursor = *ll.NextCursor
	}
	as.mu.Lock()
	for id := range as.lastReleased {
		if !loaded[id] {
			as.deletes = append(as.deletes, id)
			as.gone[id] = as.now()
		}
	}
	as.lastReleased = nil
	as.mu.Unlock()
	err = as.worker(sctx, c)
	slog.Info("codex app-server disconnected", "socket", as.sock, "err", err)
	return err
}

func (as *appServer) call(ctx context.Context, c *rpcConn, method string, params, out any) error {
	cctx, cancel := context.WithTimeout(ctx, as.callTimeout)
	defer cancel()
	return c.call(cctx, method, params, out)
}

func (as *appServer) disconnect() {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.lastReleased = map[string]bool{}
	for id, t := range as.threads {
		if t.post.Posted() {
			as.released = append(as.released, id)
			as.lastReleased[id] = true
		}
	}
	as.connected, as.userAgent = false, ""
	as.threads = map[string]*apThread{}
	as.unread = map[string]*apPending{}
	as.ephemeral = map[string]time.Time{}
	as.hasRate, as.rate = false, derived{}
}

func (as *appServer) worker(ctx context.Context, c *rpcConn) error {
	live := time.NewTicker(as.liveEvery)
	defer live.Stop()
	for {
		next := as.reconcile(ctx, c)
		var retry <-chan time.Time
		var timer *time.Timer
		if !next.IsZero() {
			timer = time.NewTimer(time.Until(next))
			retry = timer.C
		}
		check := false
		select {
		case <-ctx.Done():
		case <-c.done:
		case <-as.kick:
		case <-retry:
		case <-live.C:
			check = time.Since(c.lastReadAt()) >= as.liveEvery
		}
		if timer != nil {
			timer.Stop()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-c.done:
			return c.closeErr()
		default:
		}
		if check {
			lctx, cancel := context.WithTimeout(ctx, as.liveTimeout)
			err := c.call(lctx, "thread/loaded/list", map[string]any{"limit": 1}, nil)
			cancel()
			if err != nil {
				return fmt.Errorf("daemon not answering: %w", err)
			}
		}
	}
}

type apAction struct {
	kind string
	id   string
}

func (as *appServer) nextAction(now time.Time) (a apAction, retryAt time.Time) {
	as.mu.Lock()
	defer as.mu.Unlock()
	for id, p := range as.unread {
		if !p.inflight {
			p.inflight = true
			return apAction{"read", id}, time.Time{}
		}
	}
	for id, t := range as.threads {
		switch {
		case t.busy && t.tracked && !t.subscribed:
			if now.Before(t.resumeAt) {
				if retryAt.IsZero() || t.resumeAt.Before(retryAt) {
					retryAt = t.resumeAt
				}
				continue
			}
			return apAction{"resume", id}, time.Time{}
		case !t.busy && t.subscribed:
			return apAction{"unsubscribe", id}, time.Time{}
		}
	}
	return apAction{}, retryAt
}

func (as *appServer) reconcile(ctx context.Context, c *rpcConn) time.Time {
	for ctx.Err() == nil {
		a, retryAt := as.nextAction(time.Now())
		if a.kind == "" {
			return retryAt
		}
		switch a.kind {
		case "read":
			var res struct {
				Thread wireThread `json:"thread"`
			}
			err := as.call(ctx, c, "thread/read", map[string]any{"threadId": a.id}, &res)
			as.mu.Lock()
			p := as.unread[a.id]
			delete(as.unread, a.id)
			if err == nil && p != nil && !p.closed {
				as.addThreadLocked(res.Thread)
				if t := as.threads[a.id]; t != nil && p.status != nil {
					as.applyStatusLocked(t, *p.status)
				}
			}
			as.mu.Unlock()
		case "resume":
			err := as.call(ctx, c, "thread/resume", map[string]any{"threadId": a.id, "excludeTurns": true}, nil)
			as.mu.Lock()
			if t := as.threads[a.id]; t != nil {
				if err == nil {
					t.subscribed = true
				} else {
					t.resumeAt = time.Now().Add(as.retryEvery)
				}
			}
			as.mu.Unlock()
		case "unsubscribe":
			_ = as.call(ctx, c, "thread/unsubscribe", map[string]any{"threadId": a.id}, nil)
			as.mu.Lock()
			if t := as.threads[a.id]; t != nil {
				t.subscribed = false
			}
			as.mu.Unlock()
		}
	}
	return time.Time{}
}

type wireStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

type wireThread struct {
	ID         string          `json:"id"`
	Ephemeral  bool            `json:"ephemeral"`
	Source     json.RawMessage `json:"source"`
	Originator *string         `json:"originator"`
	Preview    string          `json:"preview"`
	UpdatedAt  int64           `json:"updatedAt"`
	Status     wireStatus      `json:"status"`
}

func (as *appServer) addThreadLocked(th wireThread) {
	if th.ID == "" || as.threads[th.ID] != nil {
		return
	}
	if th.Ephemeral {
		as.ephemeral[th.ID] = as.now()
		return
	}
	if th.Status.Type == "notLoaded" {
		return
	}
	meta := sessionMeta{id: th.ID, source: strings.ToLower(sourceKind(th.Source))}
	if th.Originator != nil {
		meta.originator = *th.Originator
	}
	t := &apThread{id: th.ID, tracked: as.cfg.tracks(meta), viaClaude: meta.originator == claudeOriginator}
	t.lastChange = as.now()
	if th.UpdatedAt > 0 {
		t.lastChange = time.Unix(th.UpdatedAt, 0)
	}
	switch th.Status.Type {
	case "idle":
		if th.Preview != "" {
			t.d.state = "done"
		}
	case "systemError":
		t.d.state = "error"
	case "active":
		as.applyStatusLocked(t, th.Status)
	}
	delete(as.gone, th.ID)
	as.threads[th.ID] = t
	as.wake()
}

func (as *appServer) applyStatusLocked(t *apThread, st wireStatus) {
	switch st.Type {
	case "active":
		if !t.busy {
			t.failed = false
			if as.cfg.ActivityTrailEnabled {
				t.d.activity = ""
			}
		}
		t.busy = true
		t.d.state = "running"
		for _, f := range st.ActiveFlags {
			if f == "waitingOnApproval" || f == "waitingOnUserInput" {
				t.d.state = "waiting"
			}
		}
	case "idle":
		if t.busy || t.d.state != "" {
			t.d.state = "done"
			if t.failed {
				t.d.state = "error"
			}
		}
		t.busy = false
	case "systemError":
		t.busy = false
		t.failed = true
		t.d.state = "error"
	}
	t.lastChange = as.now()
}

func (as *appServer) closeLocked(id string) {
	t := as.threads[id]
	delete(as.threads, id)
	if p := as.unread[id]; p != nil && p.inflight {
		p.closed = true // the in-flight read must not resurrect it
	} else {
		delete(as.unread, id)
	}
	delete(as.ephemeral, id)
	if t == nil {
		return
	}
	as.gone[id] = as.now()
	if t.post.Posted() {
		as.deletes = append(as.deletes, id)
	}
}

type turnPayload struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		Status string           `json:"status"`
		Items  []wireThreadItem `json:"items"`
	} `json:"turn"`
}

type wireThreadItem struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Command string `json:"command"`
	Status  string `json:"status"`
	Server  string `json:"server"`
	Tool    string `json:"tool"`
	Query   string `json:"query"`
	Changes []struct {
		Path string `json:"path"`
	} `json:"changes"`
}

type rateLimitWindow struct {
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    *int64  `json:"resetsAt"`
}

func (w rateLimitWindow) resetsAt() int64 {
	if w.ResetsAt == nil {
		return 0
	}
	return *w.ResetsAt
}

func (as *appServer) onNotification(method string, params json.RawMessage) {
	as.mu.Lock()
	defer as.mu.Unlock()
	switch method {
	case "thread/started":
		var p struct {
			Thread wireThread `json:"thread"`
		}
		if json.Unmarshal(params, &p) == nil {
			as.addThreadLocked(p.Thread)
		}
	case "thread/status/changed":
		var p struct {
			ThreadID string     `json:"threadId"`
			Status   wireStatus `json:"status"`
		}
		if json.Unmarshal(params, &p) != nil || p.ThreadID == "" {
			return
		}
		if _, eph := as.ephemeral[p.ThreadID]; eph {
			return
		}
		if p.Status.Type == "notLoaded" {
			as.closeLocked(p.ThreadID)
			return
		}
		t := as.threads[p.ThreadID]
		if t == nil {
			pend := as.unread[p.ThreadID]
			if pend == nil {
				pend = &apPending{}
				as.unread[p.ThreadID] = pend
			}
			if pend.inflight {
				st := p.Status
				pend.status = &st
			}
			as.wake()
			return
		}
		as.applyStatusLocked(t, p.Status)
		as.wake()
	case "thread/closed":
		var p struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(params, &p) == nil {
			as.closeLocked(p.ThreadID)
		}
	case "turn/started":
		var p turnPayload
		if json.Unmarshal(params, &p) != nil {
			return
		}
		if t := as.threads[p.ThreadID]; t != nil {
			t.failed = false
			t.d.state = "running"
			if as.cfg.ActivityTrailEnabled {
				t.d.activity = ""
			}
			t.lastChange = as.now()
		}
	case "turn/completed":
		var p turnPayload
		if json.Unmarshal(params, &p) != nil {
			return
		}
		t := as.threads[p.ThreadID]
		if t == nil {
			return
		}
		switch p.Turn.Status {
		case "failed":
			t.failed = true
			t.d.state = "error"
		case "completed", "interrupted":
			t.d.state = "done"
		}
		for i := len(p.Turn.Items) - 1; i >= 0; i-- {
			if it := p.Turn.Items[i]; it.Type == "agentMessage" && strings.TrimSpace(it.Text) != "" {
				t.d.message = truncate(strings.TrimSpace(it.Text), 80)
				break
			}
		}
		t.lastChange = as.now()
	case "item/started", "item/completed":
		var p struct {
			ThreadID string         `json:"threadId"`
			Item     wireThreadItem `json:"item"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		t := as.threads[p.ThreadID]
		if t == nil {
			return
		}
		if method == "item/completed" {
			if p.Item.Type == "agentMessage" {
				if m := strings.TrimSpace(p.Item.Text); m != "" {
					t.d.message = truncate(m, 80)
				}
			}
			return
		}
		if label, ok := appServerItemLabel(p.Item); ok && as.cfg.ActivityTrailEnabled {
			t.d.activity = producer.PrependTrail(label, t.d.activity)
		}
	case "thread/tokenUsage/updated":
		var p struct {
			ThreadID   string `json:"threadId"`
			TokenUsage struct {
				Last struct {
					InputTokens int64 `json:"inputTokens"`
				} `json:"last"`
				ModelContextWindow int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		t := as.threads[p.ThreadID]
		if t == nil || !as.cfg.ContextPctEnabled || p.TokenUsage.ModelContextWindow <= 0 {
			return
		}
		pct := producer.Pct(100 * float64(p.TokenUsage.Last.InputTokens) / float64(p.TokenUsage.ModelContextWindow))
		t.d.contextPct = &pct
	case "account/rateLimits/updated":
		var p struct {
			RateLimits struct {
				LimitID   *string          `json:"limitId"`
				Primary   *rateLimitWindow `json:"primary"`
				Secondary *rateLimitWindow `json:"secondary"`
			} `json:"rateLimits"`
		}
		if json.Unmarshal(params, &p) != nil || !as.cfg.RatePctEnabled {
			return
		}
		lim := p.RateLimits
		if lim.LimitID != nil && *lim.LimitID != "" && *lim.LimitID != "codex" {
			return
		}
		if w := lim.Primary; w != nil {
			r := producer.Pct(w.UsedPercent)
			as.rate.rateWindowPct, as.rate.rateResetAt, as.rate.primaryRaw = &r, w.resetsAt(), w.UsedPercent
			as.hasRate = true
		}
		if w := lim.Secondary; w != nil {
			wk := producer.Pct(w.UsedPercent)
			as.rate.weeklyPct, as.rate.weeklyResetAt, as.rate.weeklyRaw = &wk, w.resetsAt(), w.UsedPercent
			as.hasRate = true
		}
		as.rate.rateAt = as.now()
	}
}

func appServerItemLabel(it wireThreadItem) (string, bool) {
	switch it.Type {
	case "commandExecution":
		return execLabel(unwrapShell(it.Command)), true
	case "fileChange":
		changes := map[string]json.RawMessage{}
		for _, c := range it.Changes {
			changes[c.Path] = nil
		}
		return editLabel(changes), true
	case "mcpToolCall":
		return prefixed("mcp", it.Tool), true
	case "dynamicToolCall":
		return prefixed("tool", it.Tool), true
	case "webSearch":
		return prefixed("web", it.Query), true
	case "collabAgentToolCall":
		return prefixed("agent", it.Tool), true
	case "sleep":
		return "sleep", true
	case "contextCompaction":
		return "compact", true
	}
	return "", false
}

func unwrapShell(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	f := strings.SplitN(cmd, " ", 3)
	if len(f) == 3 && (f[1] == "-lc" || f[1] == "-c") {
		switch filepath.Base(f[0]) {
		case "sh", "bash", "zsh", "dash", "fish":
			body := strings.TrimSpace(f[2])
			if len(body) >= 2 && (body[0] == '\'' || body[0] == '"') && body[len(body)-1] == body[0] {
				body = body[1 : len(body)-1]
			}
			return firstLine(body)
		}
	}
	return firstLine(cmd)
}

type apTick struct {
	posts    []producer.StatusRequest
	deletes  []producer.DeleteRequest
	owned    map[string]bool
	released []string
	held     map[string]bool
	rate     *derived
}

func (as *appServer) tick() apTick {
	now := as.now()
	window := time.Duration(as.cfg.ActivityWindowSeconds) * time.Second
	as.mu.Lock()
	defer as.mu.Unlock()
	var out apTick
	as.rate.expireWindows(now)
	for id, t := range as.threads {
		if !t.tracked || t.d.state == "" {
			continue
		}
		if !t.busy && now.Sub(t.lastChange) > window {
			if t.post.Posted() {
				out.deletes = append(out.deletes, producer.DeleteRequest{Source: as.cfg.Source, Tool: "codex", Session: id})
				t.post.Reset()
			}
			continue
		}
		d := t.d
		d.rateWindowPct, d.rateResetAt = as.rate.rateWindowPct, as.rate.rateResetAt
		if t.post.Due(fingerprint(d), now) {
			req := buildStatusRequest(as.cfg, id, d)
			if t.viaClaude {
				req.Message = viaClaudeMessage(req.Message)
			}
			out.posts = append(out.posts, req)
		}
	}
	for _, id := range as.deletes {
		out.deletes = append(out.deletes, producer.DeleteRequest{Source: as.cfg.Source, Tool: "codex", Session: id})
	}
	as.deletes = nil
	out.released, as.released = as.released, nil
	out.owned, out.held = map[string]bool{}, map[string]bool{}
	for id, t := range as.threads {
		out.owned[id] = true
		if t.post.Posted() {
			out.held[id] = true
		}
	}
	for id, at := range as.ephemeral {
		if now.Sub(at) > ephemeralTTL {
			delete(as.ephemeral, id)
		}
	}
	for id, at := range as.gone {
		if now.Sub(at) > window+resumeScanInterval {
			delete(as.gone, id)
			continue
		}
		out.owned[id] = true
	}
	if as.connected && as.hasRate {
		r := as.rate
		out.rate = &r
	}
	return out
}

func (as *appServer) status() (connected bool, userAgent string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.connected, as.userAgent
}
