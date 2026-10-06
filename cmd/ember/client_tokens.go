package main

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Client token scopes. A client (kind "client", token prefix ekc_) holds a
// subset; the master EMBER_TOKEN and the admin scope satisfy every one.
const (
	scopeIngest  = "ingest"
	scopeControl = "control"
	scopeRead    = "read"
	scopeAdmin   = "admin"
)

// maxClients bounds the client records; every mint rewrites the registry blob.
const maxClients = 64

var (
	errScopeDenied    = errors.New("token lacks the required scope")
	errMasterRequired = errors.New("managing client tokens requires EMBER_TOKEN")
	errTooManyClients = fmt.Errorf("%w: at most %d client tokens; delete one first", errDeviceBody, maxClients)
)

type clientCallerKey struct{}

// requireMasterForClients answers 403 and returns false when a client token
// (even an admin one) tries to mint, rotate or delete a client: a leaked
// client must not be able to outlive its own revocation.
func (a *App) requireMasterForClients(w http.ResponseWriter, r *http.Request) bool {
	if isClient, _ := r.Context().Value(clientCallerKey{}).(bool); !isClient {
		return true
	}
	a.logger.InfoContext(r.Context(), "auth scope denied", "reason", "master_required", "path", r.URL.Path, "method", r.Method)
	writeError(w, http.StatusForbidden, errMasterRequired)
	return false
}

// requiredScope is the client scope that admits an owner route (a pattern
// registered on the authenticated /v1 mux). Anything not listed needs admin.
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

// normalizeScopes validates a mint request's scopes and returns them sorted
// and deduplicated; at least one is required.
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

// clientAuth checks a non-master bearer against the client registry. It
// answers 401 for an unknown token, 403 for a client without scope, 500 when
// the registry is unreadable, and returns true only when next may run.
func (a *App) clientAuth(w http.ResponseWriter, r *http.Request, scope string) bool {
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	id, scopes, ok, err := a.devices.authenticateClient(bearer)
	if err != nil {
		a.logger.WarnContext(r.Context(), "client auth failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("device registry unavailable"))
		return false
	}
	if !ok {
		a.logger.InfoContext(r.Context(), "auth rejected",
			"remote_addr", r.RemoteAddr, "path", r.URL.Path, "method", r.Method)
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return false
	}
	if !scopeAllowed(scopes, scope) {
		a.logger.InfoContext(r.Context(), "auth scope denied",
			"client_id", id, "scope", scope, "path", r.URL.Path, "method", r.Method)
		writeError(w, http.StatusForbidden, fmt.Errorf("%w %q", errScopeDenied, scope))
		return false
	}
	return true
}

func isClientBearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+clientTokenPrefix)
}
