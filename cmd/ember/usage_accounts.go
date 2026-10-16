package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	usageAccountsMax         = 32
	usageAccountNameMax      = 24
	usageAccountOrderMax     = 999
)

var (
	accountKeyRe = regexp.MustCompile(`^[0-9a-f]{16}$`)
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
	Tool  string    `json:"tool"`
	Plan  string    `json:"plan,omitempty"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

type usageAccountRegistry struct {
	mu   sync.Mutex
	seen map[string]usageAccountSeen
	kv   func() settingsKV
}

func newUsageAccountRegistry(kv func() settingsKV) *usageAccountRegistry {
	return &usageAccountRegistry{seen: map[string]usageAccountSeen{}, kv: kv}
}

func (r *usageAccountRegistry) load() error {
	kv := r.kv()
	if kv == nil {
		return nil
	}
	blob, ok, err := kv.GetSetting(usageAccountsSeenKey)
	if err != nil || !ok {
		return err
	}
	seen := map[string]usageAccountSeen{}
	if err := json.Unmarshal([]byte(blob), &seen); err != nil {
		return fmt.Errorf("parse %s: %w", usageAccountsSeenKey, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = seen
	return nil
}

func (r *usageAccountRegistry) observe(k usageKey, plan string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := k.id()
	prev, ok := r.seen[id]
	if ok && prev.Tool == k.Tool && prev.Plan == plan && now.Sub(prev.Last) < usageAccountSeenEvery && !now.Before(prev.Last) {
		return nil
	}
	next := usageAccountSeen{Tool: k.Tool, Plan: plan, First: now, Last: now}
	if ok && prev.First.Before(now) {
		next.First = prev.First
	}
	r.seen[id] = next
	r.pruneLocked(now)
	kv := r.kv()
	if kv == nil {
		return nil
	}
	b, err := json.Marshal(r.seen)
	if err != nil {
		return err
	}
	return kv.PutSetting(usageAccountsSeenKey, string(b))
}

func (r *usageAccountRegistry) pruneLocked(now time.Time) {
	for id, s := range r.seen {
		if now.Sub(s.Last) > usageAccountTTL {
			delete(r.seen, id)
		}
	}
	for len(r.seen) > usageAccountsMax {
		oldest := ""
		for id, s := range r.seen {
			if o := r.seen[oldest]; oldest == "" || s.Last.Before(o.Last) || (s.Last.Equal(o.Last) && id < oldest) {
				oldest = id
			}
		}
		delete(r.seen, oldest)
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
	if !toolNameRe.MatchString(id) {
		return p, fmt.Errorf("account id %q must match ^[a-z0-9_-]{1,32}$", id)
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
			if len(out) > 2*usageAccountsMax {
				return fmt.Errorf("at most %d account settings", 2*usageAccountsMax)
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
	DefaultName string
	Hidden      bool
	Order       *int
	FirstSeen   time.Time
	Report      *ToolUsage
	Fresh       bool
}

func buildUsageAccounts(state usageState, seen map[string]usageAccountSeen, prefs map[string]usageAccountPrefs) []usageAccount {
	byID := map[string]*usageAccount{}
	for _, s := range state.Accounts {
		rep := s.Report
		byID[s.Key.id()] = &usageAccount{ID: s.Key.id(), Tool: s.Key.Tool, Plan: rep.Plan, FirstSeen: rep.UpdatedAt, Report: &rep, Fresh: s.Fresh}
	}
	for id, s := range seen {
		acc, ok := byID[id]
		if !ok {
			byID[id] = &usageAccount{ID: id, Tool: s.Tool, Plan: s.Plan, FirstSeen: s.First}
			continue
		}
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
			x.FirstSeen.Compare(y.FirstSeen), cmp.Compare(x.ID, y.ID))
	})
	for i := range out {
		acc := &out[i]
		base := usageBaseName(acc.Tool, acc.Plan)
		pos, twins := 0, 0
		for j, other := range out {
			if other.Tool != acc.Tool || usageBaseName(other.Tool, other.Plan) != base || (j != i && other.Name != "") {
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
		if out[i].Name == "" {
			out[i].Name = out[i].DefaultName
		}
	}
	return out
}

func (a *App) usageAccounts(now time.Time) []usageAccount {
	return buildUsageAccounts(a.usage.state(nil, now), a.usageSeen.snapshot(now), a.cfg.Load().UsageAccounts)
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
			ID: acc.ID, Tool: acc.Tool, Plan: optString(acc.Plan), Name: acc.Name, DefaultName: acc.DefaultName,
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
