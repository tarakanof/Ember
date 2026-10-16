package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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

func claudeConfigSig() (path, sig string) {
	path = claudeGlobalConfigPath()
	if path == "" {
		return "", ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", ""
	}
	return path, path + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + "|" + strconv.FormatInt(fi.Size(), 10)
}

func readClaudeAccount(prevSig string, prev claudeAccount) (claudeAccount, string) {
	path, sig := claudeConfigSig()
	if sig == "" {
		return claudeAccount{}, ""
	}
	if sig == prevSig {
		return prev, sig
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return claudeAccount{}, ""
	}
	return parseClaudeAccount(b), sig
}

type claudeAccountCache struct {
	mu   sync.Mutex
	sig  string
	acct claudeAccount
}

var claudeAccounts = &claudeAccountCache{}

func (c *claudeAccountCache) current() claudeAccount {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acct, c.sig = readClaudeAccount(c.sig, c.acct)
	return c.acct
}

type markerAccount struct {
	UsageAccountKey string `json:"usage_account_key,omitempty"`
	UsagePlan       string `json:"usage_plan,omitempty"`
	UsageAccountSig string `json:"usage_account_sig,omitempty"`
}

func (m markerAccount) account() claudeAccount {
	return claudeAccount{key: m.UsageAccountKey, plan: m.UsagePlan}
}

func sessionClaudeAccount(prev markerAccount) markerAccount {
	a, sig := readClaudeAccount(prev.UsageAccountSig, prev.account())
	return markerAccount{UsageAccountKey: a.key, UsagePlan: a.plan, UsageAccountSig: sig}
}

func (a claudeAccount) apply(req *producer.UsageRequest) {
	req.AccountKey, req.Plan = a.key, a.plan
}
