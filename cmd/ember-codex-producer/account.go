package main

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type codexAccount struct {
	key  string
	plan string
}

type codexAuthClaims struct {
	PlanType  string `json:"chatgpt_plan_type"`
	UserID    string `json:"chatgpt_user_id"`
	AccountID string `json:"chatgpt_account_id"`
}

var codexWorkspacePlans = map[string]bool{"team": true, "business": true, "enterprise": true, "edu": true}

func idTokenClaims(token string) codexAuthClaims {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return codexAuthClaims{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return codexAuthClaims{}
	}
	var doc struct {
		Auth codexAuthClaims `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &doc) != nil {
		return codexAuthClaims{}
	}
	return doc.Auth
}

func parseCodexAuth(b []byte) codexAccount {
	var doc struct {
		Tokens *struct {
			IDToken   string `json:"id_token"`
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.Tokens == nil {
		return codexAccount{}
	}
	claims := idTokenClaims(doc.Tokens.IDToken)
	accountID := doc.Tokens.AccountID
	if accountID == "" {
		accountID = claims.AccountID
	}
	plan := producer.Plan(claims.PlanType)
	ids := []string{accountID}
	if codexWorkspacePlans[plan] && claims.UserID != "" {
		ids = append(ids, claims.UserID)
	}
	key := producer.AccountKey("codex", ids...)
	if key == "" {
		return codexAccount{}
	}
	return codexAccount{key: key, plan: plan}
}

type codexAccountCache struct {
	mu    sync.Mutex
	path  string
	mtime time.Time
	size  int64
	acct  codexAccount
}

func (c *codexAccountCache) current(codexHome string) codexAccount {
	if codexHome == "" {
		return codexAccount{}
	}
	path := filepath.Join(codexHome, "auth.json")
	fi, err := os.Stat(path)
	if err != nil {
		return codexAccount{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if path == c.path && fi.ModTime().Equal(c.mtime) && fi.Size() == c.size {
		return c.acct
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return codexAccount{}
	}
	c.path, c.mtime, c.size, c.acct = path, fi.ModTime(), fi.Size(), parseCodexAuth(b)
	return c.acct
}

var statFile, lstatFile = os.Stat, os.Lstat

func authBoundary(codexHome string) (time.Time, bool) {
	if codexHome == "" {
		return time.Time{}, false
	}
	path := filepath.Join(codexHome, "auth.json")
	fi, err := statFile(path)
	if err != nil {
		return time.Time{}, false
	}
	at := fi.ModTime()
	if c := changeTime(fi); c.After(at) {
		at = c
	}
	if li, err := lstatFile(path); err == nil && li.ModTime().After(at) {
		at = li.ModTime()
	}
	return at, true
}

const (
	maxAccountEras      = 8
	historySaveInterval = 10 * time.Minute
	accountHistoryFile  = "codex-accounts.json"
)

type accountEra struct {
	Key   string    `json:"key"`
	Since time.Time `json:"since"`
}

type accountHistoryDoc struct {
	Eras      []accountEra `json:"eras"`
	CheckedAt time.Time    `json:"checked_at"`
}

type accountHistory struct {
	path      string
	loaded    bool
	eras      []accountEra
	checkedAt time.Time
	savedAt   time.Time
	rekeyed   bool
}

func (h *accountHistory) load() {
	h.loaded = true
	if h.path == "" {
		return
	}
	b, err := os.ReadFile(h.path)
	if err != nil {
		return
	}
	var doc accountHistoryDoc
	if json.Unmarshal(b, &doc) != nil || len(doc.Eras) == 0 {
		return
	}
	if len(doc.Eras) > maxAccountEras {
		doc.Eras = doc.Eras[len(doc.Eras)-maxAccountEras:]
	}
	h.eras, h.checkedAt = doc.Eras, doc.CheckedAt
}

func (h *accountHistory) save(now time.Time) {
	h.savedAt = now
	if h.path == "" || h.unknownStart() {
		return
	}
	b, err := json.Marshal(accountHistoryDoc{Eras: h.eras, CheckedAt: h.checkedAt})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		slog.Debug("codex account history not saved", "err", err)
		return
	}
	if err := producer.WriteFileAtomic(h.path, b, 0o600); err != nil {
		slog.Debug("codex account history not saved", "err", err)
	}
}

func (h *accountHistory) observe(key string, boundary time.Time, haveBoundary bool, now time.Time) (since time.Time, switched bool) {
	if !h.loaded {
		h.load()
	}
	dirty := false
	switch n := len(h.eras); {
	case n == 0:
		h.eras = []accountEra{{Key: key}}
		dirty = true
	case n == 1 && h.unknownStart() && key != "":
		h.eras[0].Key = key
		dirty, h.rekeyed = true, true
	case key == "" && h.eras[n-1].Key != "":
	case h.eras[n-1].Key != key:
		since = h.switchTime(boundary, haveBoundary, now)
		h.eras = append(h.eras, accountEra{Key: key, Since: since})
		if len(h.eras) > maxAccountEras {
			h.eras = h.eras[len(h.eras)-maxAccountEras:]
		}
		switched, dirty = true, true
	}
	h.checkedAt = now
	if dirty || now.Sub(h.savedAt) >= historySaveInterval || now.Before(h.savedAt) {
		h.save(now)
	}
	return since, switched
}

func (h *accountHistory) switchTime(boundary time.Time, haveBoundary bool, now time.Time) time.Time {
	since := now
	if haveBoundary && boundary.Before(now) {
		since = boundary
	}
	if h.checkedAt.After(since) && !h.checkedAt.After(now) {
		since = h.checkedAt
	}
	return since
}

func (h *accountHistory) unknownStart() bool {
	return len(h.eras) == 1 && h.eras[0].Key == "" && h.eras[0].Since.IsZero()
}

func (h *accountHistory) starts() []time.Time {
	var out []time.Time
	for _, e := range h.eras {
		if !e.Since.IsZero() {
			out = append(out, e.Since)
		}
	}
	return out
}

func (h *accountHistory) current() string {
	if len(h.eras) == 0 {
		return ""
	}
	return h.eras[len(h.eras)-1].Key
}

func (h *accountHistory) at(t time.Time) (string, bool) {
	if t.IsZero() {
		return "", false
	}
	for i := len(h.eras) - 1; i >= 0; i-- {
		if !t.Before(h.eras[i].Since) {
			return h.eras[i].Key, true
		}
	}
	return "", false
}
