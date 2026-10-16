package producer

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

const accountKeyBytes = 8

var planPattern = regexp.MustCompile(`^[a-z0-9_]{1,24}$`)

// AccountKey returns a 16-hex-character hash of tool and ids, or "" when the first id is empty.
func AccountKey(tool string, ids ...string) string {
	if len(ids) == 0 || strings.TrimSpace(ids[0]) == "" {
		return ""
	}
	parts := make([]string, 0, len(ids)+1)
	parts = append(parts, tool)
	for _, id := range ids {
		parts = append(parts, strings.TrimSpace(id))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:accountKeyBytes])
}

// Plan lower-cases raw and returns it when it is a valid plan token for POST /v1/usage, else "".
func Plan(raw string) string {
	p := strings.ToLower(strings.TrimSpace(raw))
	p = strings.NewReplacer("-", "_", " ", "_").Replace(p)
	if !planPattern.MatchString(p) {
		return ""
	}
	return p
}
