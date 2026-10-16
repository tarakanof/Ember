package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type claudeAccount struct {
	key  string
	plan string
}

func claudeGlobalConfigPath() string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	home, err := os.UserHomeDir()
	if dir == "" && err != nil {
		return ""
	}
	legacy := filepath.Join(home, ".claude", ".config.json")
	current := filepath.Join(home, ".claude.json")
	if dir != "" {
		legacy = filepath.Join(dir, ".config.json")
		current = filepath.Join(dir, ".claude.json")
	}
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return current
}

func parseClaudeAccount(b []byte) claudeAccount {
	type tierFields struct {
		RateLimitTier    string `json:"organizationRateLimitTier"`
		OrganizationType string `json:"organizationType"`
	}
	var doc struct {
		tierFields
		OAuth *struct {
			AccountUUID      string `json:"accountUuid"`
			OrganizationUUID string `json:"organizationUuid"`
			tierFields
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.OAuth == nil {
		return claudeAccount{}
	}
	a := claudeAccount{key: producer.AccountKey("claude", doc.OAuth.AccountUUID, doc.OAuth.OrganizationUUID)}
	if a.key == "" {
		return claudeAccount{}
	}
	a.plan = claudePlan(doc.OAuth.OrganizationType, doc.OAuth.RateLimitTier, doc.OrganizationType, doc.RateLimitTier)
	return a
}

func claudePlan(raw ...string) string {
	for _, r := range raw {
		s := strings.ToLower(r)
		for _, plan := range []string{"max", "enterprise", "team", "pro", "free"} {
			if strings.Contains(s, plan) {
				return plan
			}
		}
	}
	return ""
}

type claudeAccountCache struct {
	mu    sync.Mutex
	path  string
	mtime time.Time
	size  int64
	acct  claudeAccount
}

var claudeAccounts = &claudeAccountCache{}

func (c *claudeAccountCache) current() claudeAccount {
	path := claudeGlobalConfigPath()
	fi, err := os.Stat(path)
	if path == "" || err != nil {
		return claudeAccount{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if path == c.path && fi.ModTime().Equal(c.mtime) && fi.Size() == c.size {
		return c.acct
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return claudeAccount{}
	}
	c.path, c.mtime, c.size, c.acct = path, fi.ModTime(), fi.Size(), parseClaudeAccount(b)
	return c.acct
}

func (a claudeAccount) apply(req *producer.UsageRequest) {
	req.AccountKey, req.Plan = a.key, a.plan
}
