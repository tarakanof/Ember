package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// validateSetting validates and normalizes a single producer.env value,
// returning the value to persist or an error explaining the rejection.
// The token's "blank = keep current" rule is the caller's responsibility,
// not this function's.
func validateSetting(key, value string) (string, error) {
	v := strings.TrimSpace(value)
	if strings.ContainsAny(v, ctrlChars) {
		return "", fmt.Errorf("value may not contain control characters")
	}
	switch key {
	case "STATUS_SOURCE":
		if v == "" {
			return "", fmt.Errorf("source must not be empty")
		}
		return v, nil
	case "STATUS_SERVER_URL":
		if v == "" {
			return "", fmt.Errorf("server URL must not be empty")
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return "", fmt.Errorf("must be an http(s) URL with a host and no embedded credentials")
		}
		return v, nil
	case "STATUS_TOKEN":
		return v, nil // blank-handling is the caller's job
	case "STATUS_SOURCE_COLOR":
		if v == "" {
			return "", nil // unset = no tint
		}
		if !hexColorRe.MatchString(v) {
			return "", fmt.Errorf("color must be #RRGGBB hex")
		}
		return v, nil
	case "STATUS_CONTEXT_WINDOW_TOKENS":
		if v == "" {
			return "", nil // unset = model default
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return "", fmt.Errorf("context window must be a non-negative integer")
		}
		return strconv.Itoa(n), nil
	default:
		return v, nil
	}
}

// isEnvTrue mirrors the producer's STATUS_CONTEXT_PCT_ENABLED parsing:
// default true; only false/0/no/off (case-insensitive) disable it.
func isEnvTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return false
	default:
		return true // includes "" — the producer's default is true
	}
}
