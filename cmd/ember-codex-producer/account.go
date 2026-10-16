package main

import (
	"encoding/base64"
	"encoding/json"
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

func (c *codexAccountCache) changedAt(codexHome string, now time.Time) time.Time {
	if codexHome != "" {
		if fi, err := os.Stat(filepath.Join(codexHome, "auth.json")); err == nil && !fi.ModTime().After(now) {
			return fi.ModTime()
		}
	}
	return now
}
