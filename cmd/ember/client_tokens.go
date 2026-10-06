package main

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	scopeIngest  = "ingest"
	scopeControl = "control"
	scopeRead    = "read"
	scopeAdmin   = "admin"
)

const (
	maxClients       = 64
	maxClientSources = 16
	maxSourceRunes   = 64
)

var (
	errScopeDenied    = errors.New("token lacks the required scope")
	errMasterRequired = errors.New("managing client tokens requires EMBER_TOKEN")
	errTooManyClients = fmt.Errorf("%w: at most %d client tokens; delete one first", errDeviceBody, maxClients)
	errSourceDenied   = errors.New("token is not bound to source")
)

type clientCallerKey struct{}

type clientCaller struct {
	id      string
	sources []string
}

// A leaked client must not outlive its own revocation, so a client token (even admin) can never manage clients.
func (a *App) requireMasterForClients(w http.ResponseWriter, r *http.Request) bool {
	if _, isClient := r.Context().Value(clientCallerKey{}).(clientCaller); !isClient {
		return true
	}
	a.logger.InfoContext(r.Context(), "auth scope denied", "reason", "master_required", "path", r.URL.Path, "method", r.Method)
	writeError(w, http.StatusForbidden, errMasterRequired)
	return false
}

func requiredScope(pattern string) string {
	switch pattern {
	case "POST /v1/status", "DELETE /v1/status", "POST /v1/usage", "POST /v1/notify", "POST /v1/reminders/fire":
		return scopeIngest
	case "GET /v1/apps", "GET /v1/pomodoro/config", "GET /v1/usage/config", "GET /v1/display/config",
		"GET /v1/brightness/config", "GET /v1/quiet/config", "GET /v1/clock/stats":
		return scopeRead
	default:
		return scopeAdmin
	}
}

func scopeAllowed(have []string, need string) bool {
	return slices.Contains(have, need) || slices.Contains(have, scopeAdmin)
}

func normalizeScopes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: scopes is required for a client", errDeviceBody)
	}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		switch s {
		case scopeIngest, scopeControl, scopeRead, scopeAdmin:
			out = append(out, s)
		default:
			return nil, fmt.Errorf("%w: unknown scope %q (want ingest, control, read or admin)", errDeviceBody, s)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func normalizeSources(raw, scopes []string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw) == 0 || len(raw) > maxClientSources {
		return nil, fmt.Errorf("%w: sources must list 1 to %d sources, or be omitted", errDeviceBody, maxClientSources)
	}
	if !scopeAllowed(scopes, scopeIngest) {
		return nil, fmt.Errorf("%w: sources need the ingest or admin scope", errDeviceBody)
	}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxSourceRunes {
			return nil, fmt.Errorf("%w: each source must be 1 to %d characters", errDeviceBody, maxSourceRunes)
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func (a *App) clientAuth(w http.ResponseWriter, r *http.Request, scope string) (clientCaller, bool) {
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	c, ok, err := a.devices.authenticateClient(bearer)
	if err != nil {
		a.logger.WarnContext(r.Context(), "client auth failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("device registry unavailable"))
		return clientCaller{}, false
	}
	if !ok {
		a.logger.InfoContext(r.Context(), "auth rejected",
			"remote_addr", r.RemoteAddr, "path", r.URL.Path, "method", r.Method)
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return clientCaller{}, false
	}
	if !scopeAllowed(c.scopes, scope) {
		a.logger.InfoContext(r.Context(), "auth scope denied",
			"client_id", c.id, "scope", scope, "path", r.URL.Path, "method", r.Method)
		writeError(w, http.StatusForbidden, fmt.Errorf("%w %q", errScopeDenied, scope))
		return clientCaller{}, false
	}
	return clientCaller{id: c.id, sources: c.sources}, true
}

func (a *App) allowSource(w http.ResponseWriter, r *http.Request, source string) bool {
	c, _ := r.Context().Value(clientCallerKey{}).(clientCaller)
	source = strings.TrimSpace(source)
	if len(c.sources) == 0 || slices.Contains(c.sources, source) {
		return true
	}
	a.logger.InfoContext(r.Context(), "auth source denied",
		"client_id", c.id, "source", source, "path", r.URL.Path, "method", r.Method)
	writeError(w, http.StatusForbidden, fmt.Errorf("%w %q", errSourceDenied, source))
	return false
}

func isClientBearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+clientTokenPrefix)
}
