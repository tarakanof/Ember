package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	usageAccountsSeenKey     = "usage_accounts_seen"
	usageAccountsSettingsKey = "usage_accounts_json"
	usageAccountSeenEvery    = time.Hour
	usageAccountNameMax      = 24
	usageAccountOrderMax     = 999
)

var (
	accountKeyRe = regexp.MustCompile(`^[0-9a-f]{16}$`)
	accountIDRe  = regexp.MustCompile(`^[a-z0-9_-]{1,32}(:[0-9a-f]{16})?$`)
	planRe       = regexp.MustCompile(`^[a-z0-9_]{1,24}$`)

	errUsageAccountsMasterOnly = errors.New("usage account settings need EMBER_TOKEN")
)

var usagePlanLabels = map[string]string{
	"max": "Max", "pro": "Pro", "team": "Team", "enterprise": "Enterprise", "plus": "Plus",
	"business": "Business", "free": "Free", "edu": "Edu", "go": "Go",
}

var usageToolLabels = map[string]string{"claude": "Claude", "codex": "Codex"}

func capitalize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func usageBaseName(tool, plan string) string {
	if plan != "" {
		if l, ok := usagePlanLabels[plan]; ok {
			return l
		}
		return capitalize(plan)
	}
	if l, ok := usageToolLabels[tool]; ok {
		return l
	}
	return capitalize(tool)
}

func usageToolRank(tool string) int {
	if i := slices.Index(usageTools, tool); i >= 0 {
		return i
	}
	return len(usageTools)
}

type usageAccountSeen struct {
	Tool      string         `json:"tool"`
	Principal string         `json:"principal,omitempty"`
	Plan      string         `json:"plan,omitempty"`
	First     time.Time      `json:"first"`
	Last      time.Time      `json:"last"`
	Seq       int            `json:"seq,omitempty"`
	Master    bool           `json:"master,omitempty"`
	Report    *usageSnapshot `json:"report,omitempty"`
}

type usageSnapshotWindow struct {
	Pct           float64
	ResetsAt      int64
	WindowMinutes int64
}

func (w usageSnapshotWindow) MarshalJSON() ([]byte, error) {
	return json.Marshal([3]any{w.Pct, w.ResetsAt, w.WindowMinutes})
}

func (w *usageSnapshotWindow) UnmarshalJSON(b []byte) error {
	var v [3]json.Number
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	pct, err := v[0].Float64()
	if err != nil {
		return err
	}
	at, err := v[1].Int64()
	if err != nil {
		return err
	}
	mins, err := v[2].Int64()
	if err != nil {
		return err
	}
	*w = usageSnapshotWindow{Pct: pct, ResetsAt: at, WindowMinutes: mins}
	return nil
}

type usageSnapshot struct {
	FiveHour *usageSnapshotWindow `json:"five_hour,omitempty"`
	SevenDay *usageSnapshotWindow `json:"seven_day,omitempty"`
	Models   map[string]float64   `json:"models,omitempty"`
}

func snapshotPct(p float64) float64 { return math.Round(p*10) / 10 }

func snapshotWindow(w *UsageWindow) *usageSnapshotWindow {
	if w == nil {
		return nil
	}
	return &usageSnapshotWindow{Pct: snapshotPct(w.UsedPercent), ResetsAt: w.ResetsAt, WindowMinutes: w.WindowMinutes}
}

func newUsageSnapshot(u ToolUsage) *usageSnapshot {
	s := &usageSnapshot{FiveHour: snapshotWindow(u.FiveHour), SevenDay: snapshotWindow(u.SevenDay)}
	for name, w := range u.Models {
		if w == nil || !knobUsageModelRe.MatchString(name) || len(s.Models) == usageModelsMax {
			continue
		}
		if s.Models == nil {
			s.Models = map[string]float64{}
		}
		s.Models[name] = snapshotPct(w.UsedPercent)
	}
	return s
}

func (w *usageSnapshotWindow) usage() *UsageWindow {
	if w == nil {
		return nil
	}
	return &UsageWindow{UsedPercent: w.Pct, ResetsAt: w.ResetsAt, WindowMinutes: w.WindowMinutes}
}

func (s *usageSnapshot) usage(plan string, at time.Time) ToolUsage {
	u := ToolUsage{FiveHour: s.FiveHour.usage(), SevenDay: s.SevenDay.usage(), Plan: plan, UpdatedAt: at}
	for name, pct := range s.Models {
		if u.Models == nil {
			u.Models = map[string]*UsageWindow{}
		}
		u.Models[name] = &UsageWindow{UsedPercent: pct}
	}
	return u
}

type usageAccountRegistry struct {
	mu      sync.Mutex
	seen    map[string]usageAccountSeen
	written map[string]time.Time
	loadErr error
	nextSeq int
	kv      func() settingsKV
}

func newUsageAccountRegistry(kv func() settingsKV) *usageAccountRegistry {
	return &usageAccountRegistry{seen: map[string]usageAccountSeen{}, written: map[string]time.Time{}, kv: kv}
}

func (r *usageAccountRegistry) load() error {
	kv := r.kv()
	if kv == nil {
		return nil
	}
	blob, ok, err := kv.GetSetting(usageAccountsSeenKey)
	if err == nil && !ok {
		return nil
	}
	seen := map[string]usageAccountSeen{}
	if err == nil {
		if err = json.Unmarshal([]byte(blob), &seen); err != nil {
			err = fmt.Errorf("parse %s: %w", usageAccountsSeenKey, err)
		}
	} else {
		err = fmt.Errorf("read %s: %w", usageAccountsSeenKey, err)
	}
	if err != nil {
		r.mu.Lock()
		r.loadErr = err
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = seen
	r.written = make(map[string]time.Time, len(seen))
	for id, s := range seen {
		r.written[id] = s.Last
		r.nextSeq = max(r.nextSeq, s.Seq)
	}
	return nil
}

func (r *usageAccountRegistry) observe(k usageKey, rep ToolUsage, principal string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(now)
	id := k.id()
	prev, ok := r.seen[id]
	if ok && now.Before(prev.Last) {
		return nil
	}
	if !ok {
		r.evictOwnOldestLocked(func(s usageAccountSeen) bool { return s.Tool == k.Tool }, principal, usageAccountsPerTool)
		r.evictOwnOldestLocked(func(usageAccountSeen) bool { return true }, principal, usageAccountsPerWriter)
		if len(r.seen) >= usageAccountsTotal {
			return nil
		}
	}
	plan := rep.Plan
	next := usageAccountSeen{Tool: k.Tool, Principal: principal, Plan: plan, First: now, Last: now, Report: newUsageSnapshot(rep)}
	if ok && prev.First.Before(now) {
		next.First = prev.First
	}
	next.Master = prev.Master || principal == ""
	if next.Seq = prev.Seq; !ok {
		r.nextSeq++
		next.Seq = r.nextSeq
	}
	r.seen[id] = next
	if ok && prev.Tool == k.Tool && prev.Plan == plan && prev.Principal == next.Principal && prev.Master == next.Master &&
		now.Sub(r.written[id]) < usageAccountSeenEvery {
		return nil
	}
	r.written[id] = now
	kv := r.kv()
	if kv == nil || r.loadErr != nil {
		return nil
	}
	b, err := json.Marshal(r.persistedLocked())
	if err != nil {
		return err
	}
	return kv.PutSetting(usageAccountsSeenKey, string(b))
}

func (r *usageAccountRegistry) refuses(k usageKey, writer string, alive func(string) bool, now time.Time) (owner string, masterHeld, refused bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.seen[k.id()]
	if !ok || now.Sub(s.Last) > usageAccountTTL {
		return "", false, false
	}
	owner, allowed := usageOwnerAfter(s.Principal, s.Last, writer, alive, now)
	return owner, s.Master, !allowed
}

func (r *usageAccountRegistry) persistedLocked() map[string]usageAccountSeen {
	out := make(map[string]usageAccountSeen, len(r.seen))
	for id, s := range r.seen {
		s.First, s.Last = s.First.Truncate(time.Second).UTC(), s.Last.Truncate(time.Second).UTC()
		out[id] = s
	}
	return out
}

func (r *usageAccountRegistry) expireLocked(now time.Time) {
	for id, s := range r.seen {
		if now.Sub(s.Last) > usageAccountTTL {
			r.dropLocked(id)
		}
	}
}

func (r *usageAccountRegistry) dropLocked(id string) {
	delete(r.seen, id)
	delete(r.written, id)
}

func (r *usageAccountRegistry) ids() (map[string]bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]bool, len(r.seen))
	for id := range r.seen {
		out[id] = true
	}
	return out, r.loadErr == nil
}

func (r *usageAccountRegistry) loadError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadErr
}

func (r *usageAccountRegistry) evictOwnOldestLocked(in func(usageAccountSeen) bool, principal string, max int) {
	for {
		n, oldest := 0, ""
		for id, s := range r.seen {
			if !in(s) || s.Principal != principal || (principal != "" && s.Master) {
				continue
			}
			n++
			if o := r.seen[oldest]; oldest == "" || s.Last.Before(o.Last) || (s.Last.Equal(o.Last) && id < oldest) {
				oldest = id
			}
		}
		if n < max {
			return
		}
		r.dropLocked(oldest)
	}
}

func (r *usageAccountRegistry) snapshot(now time.Time) map[string]usageAccountSeen {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]usageAccountSeen, len(r.seen))
	for id, s := range r.seen {
		if now.Sub(s.Last) <= usageAccountTTL {
			out[id] = s
		}
	}
	return out
}

type usageAccountPrefs struct {
	Name   string `json:"name,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
	Order  *int   `json:"order,omitempty"`
}

type usageAccountsDTO map[string]usageAccountPrefs

func (p *usageAccountPrefs) UnmarshalJSON(b []byte) error {
	type plain usageAccountPrefs
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var v plain
	if err := dec.Decode(&v); err != nil {
		return err
	}
	*p = usageAccountPrefs(v)
	return nil
}

func (p usageAccountPrefs) validate(id string) (usageAccountPrefs, error) {
	if !accountIDRe.MatchString(id) {
		return p, fmt.Errorf("account id %q must be a tool name, optionally followed by \":\" and its account_key", id)
	}
	p.Name = strings.TrimSpace(p.Name)
	if !utf8.ValidString(p.Name) || utf8.RuneCountInString(p.Name) > usageAccountNameMax ||
		strings.ContainsFunc(p.Name, unicode.IsControl) {
		return p, fmt.Errorf("account %s: name must be at most %d printable characters", id, usageAccountNameMax)
	}
	if p.Order != nil && (*p.Order < 0 || *p.Order > usageAccountOrderMax) {
		return p, fmt.Errorf("account %s: order %d out of range [0, %d]", id, *p.Order, usageAccountOrderMax)
	}
	return p, nil
}

func (a *App) usageAccountsSettingSpec() settingSpec[usageAccountsDTO] {
	return settingSpec[usageAccountsDTO]{
		key: usageAccountsSettingsKey,
		view: func(c Config) usageAccountsDTO {
			out := make(usageAccountsDTO, len(c.UsageAccounts))
			maps.Copy(out, c.UsageAccounts)
			return out
		},
		apply: func(c *Config, d usageAccountsDTO) error {
			out := make(map[string]usageAccountPrefs, len(d))
			for id, p := range d {
				p, err := p.validate(id)
				if err != nil {
					return err
				}
				if p != (usageAccountPrefs{}) {
					out[id] = p
				}
			}
			if len(out) > usageAccountsTotal {
				return fmt.Errorf("at most %d account settings", usageAccountsTotal)
			}
			if len(out) == 0 {
				out = nil
			}
			c.UsageAccounts = out
			return nil
		},
		check: func(cur, next usageAccountsDTO) error {
			known := map[string]bool{}
			for _, acc := range a.usageAccounts(time.Now()) {
				known[acc.ID] = true
			}
			for id, p := range next {
				if p == (usageAccountPrefs{}) {
					continue
				}
				if old, ok := cur[id]; ok && samePrefs(old, p) {
					continue
				}
				if !known[id] {
					return fmt.Errorf("unknown usage account %q", id)
				}
			}
			return nil
		},
	}
}

func samePrefs(x, y usageAccountPrefs) bool {
	return x.Name == y.Name && x.Hidden == y.Hidden && ((x.Order == nil) == (y.Order == nil)) &&
		(x.Order == nil || *x.Order == *y.Order)
}

type usageAccount struct {
	ID          string
	Tool        string
	Plan        string
	Name        string
	NameSet     bool
	DefaultName string
	Hidden      bool
	Order       *int
	FirstSeen   time.Time
	Seq         int
	Report      *ToolUsage
	Fresh       bool
}

func buildUsageAccounts(state usageState, seen map[string]usageAccountSeen, prefs map[string]usageAccountPrefs) []usageAccount {
	byID := map[string]*usageAccount{}
	for _, s := range state.Accounts {
		rep := s.Report
		byID[s.Key.id()] = &usageAccount{ID: s.Key.id(), Tool: s.Key.Tool, Plan: rep.Plan, FirstSeen: rep.FirstSeen, Report: &rep, Fresh: s.Fresh}
	}
	for id, s := range seen {
		acc, ok := byID[id]
		if !ok {
			acc = &usageAccount{ID: id, Tool: s.Tool, Plan: s.Plan, FirstSeen: s.First, Seq: s.Seq}
			if s.Report != nil {
				rep := s.Report.usage(s.Plan, s.Last)
				acc.Report = &rep
			}
			byID[id] = acc
			continue
		}
		acc.Seq = s.Seq
		if s.First.Before(acc.FirstSeen) {
			acc.FirstSeen = s.First
		}
		if acc.Plan == "" {
			acc.Plan = s.Plan
		}
	}
	keyed := map[string]bool{}
	for id, acc := range byID {
		if id != acc.Tool {
			keyed[acc.Tool] = true
		}
	}
	out := make([]usageAccount, 0, len(byID))
	for id, acc := range byID {
		if id == acc.Tool && !acc.Fresh && keyed[acc.Tool] {
			continue
		}
		p := prefs[id]
		acc.Hidden, acc.Order, acc.Name = p.Hidden, p.Order, p.Name
		out = append(out, *acc)
	}
	slices.SortFunc(out, func(x, y usageAccount) int {
		switch {
		case x.Order != nil && y.Order != nil && *x.Order != *y.Order:
			return cmp.Compare(*x.Order, *y.Order)
		case x.Order != nil && y.Order == nil:
			return -1
		case x.Order == nil && y.Order != nil:
			return 1
		}
		return cmp.Or(cmp.Compare(usageToolRank(x.Tool), usageToolRank(y.Tool)), cmp.Compare(x.Tool, y.Tool),
			x.FirstSeen.Truncate(time.Second).Compare(y.FirstSeen.Truncate(time.Second)),
			cmp.Compare(seqOrLast(x.Seq), seqOrLast(y.Seq)), cmp.Compare(x.ID, y.ID))
	})
	for i := range out {
		acc := &out[i]
		base := usageBaseName(acc.Tool, acc.Plan)
		pos, twins := 0, 0
		for j, other := range out {
			if other.Tool != acc.Tool || usageBaseName(other.Tool, other.Plan) != base || (j != i && (other.Name != "" || other.Hidden)) {
				continue
			}
			twins++
			if j == i {
				pos = twins
			}
		}
		acc.DefaultName = base
		if twins > 1 {
			acc.DefaultName = base + " " + strconv.Itoa(pos)
		}
	}
	for i := range out {
		out[i].NameSet = out[i].Name != ""
		if !out[i].NameSet {
			out[i].Name = out[i].DefaultName
		}
	}
	return out
}

func seqOrLast(seq int) int {
	if seq == 0 {
		return math.MaxInt
	}
	return seq
}

func (a *App) usageAccounts(now time.Time) []usageAccount {
	return buildUsageAccounts(a.usage.state(nil, now), a.usageSeen.snapshot(now), a.cfg.Load().UsageAccounts)
}

func (a *App) pruneUsageAccountPrefs() {
	prefs := a.cfg.Load().UsageAccounts
	if len(prefs) == 0 {
		return
	}
	known, ok := a.usageSeen.ids()
	if !ok {
		return
	}
	for k := range a.usage.All() {
		known[k.id()] = true
	}
	patch := map[string]any{}
	for id := range prefs {
		if !known[id] {
			patch[id] = nil
		}
	}
	if len(patch) == 0 {
		return
	}
	b, err := json.Marshal(patch)
	if err == nil {
		_, err = a.settings.usageAccounts.put(b)
	}
	if err != nil {
		a.logger.Warn("usage account settings prune failed", "err", err)
	}
}

func (a *App) loadUsageAccounts() {
	if err := a.usageSeen.load(); err != nil {
		a.logger.Warn("usage accounts ignored", "err", err)
	}
}

type usageAccountOut struct {
	ID          string     `json:"id"`
	Tool        string     `json:"tool"`
	Plan        *string    `json:"plan"`
	Name        string     `json:"name"`
	NameSet     bool       `json:"name_set"`
	DefaultName string     `json:"default_name"`
	Hidden      bool       `json:"hidden"`
	Order       *int       `json:"order"`
	FirstSeen   time.Time  `json:"first_seen"`
	UpdatedAt   *time.Time `json:"updated_at"`
	Stale       bool       `json:"stale"`
}

type usageAccountsOut struct {
	Accounts []usageAccountOut `json:"accounts"`
}

func (a *App) usageAccountsAnswer(now time.Time) usageAccountsOut {
	loc := now.Location()
	accs := a.usageAccounts(now)
	out := usageAccountsOut{Accounts: make([]usageAccountOut, 0, len(accs))}
	for _, acc := range accs {
		o := usageAccountOut{
			ID: acc.ID, Tool: acc.Tool, Plan: optString(acc.Plan), Name: acc.Name, NameSet: acc.NameSet, DefaultName: acc.DefaultName,
			Hidden: acc.Hidden, Order: acc.Order, FirstSeen: wireTime(acc.FirstSeen, loc), Stale: !acc.Fresh,
		}
		if acc.Report != nil {
			o.UpdatedAt = wireTimePtr(acc.Report.UpdatedAt, loc)
		}
		out.Accounts = append(out.Accounts, o)
	}
	return out
}

func (a *App) handleUsageAccountsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.usageAccountsAnswer(time.Now()))
}

func (a *App) handleUsageAccountsPut(w http.ResponseWriter, r *http.Request) {
	if !a.masterOnly(w, r, errUsageAccountsMasterOnly) {
		return
	}
	if _, ok := serveSettingPut(a, w, r, a.settings.usageAccounts); ok {
		writeJSON(w, http.StatusOK, a.usageAccountsAnswer(time.Now()))
	}
}
