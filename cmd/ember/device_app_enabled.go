package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"

	"github.com/tarakanof/ember/internal/awtrix"
)

var (
	errAppEnabledMissing = errors.New("enabled must be true or false")
	errAppNameInvalid    = errors.New("name must be 1-32 of A-Z a-z 0-9 _ - and not active, next, previous or order")
	errAppIsModule       = errors.New("name is a script module, not an app: it has no on/off switch")
	appNamePattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	reservedAppNames     = map[string]bool{"active": true, "next": true, "previous": true, "order": true}
)

type clockIdentity struct {
	base, uid, firmware string
}

type appEnabledRoute struct {
	mu      sync.Mutex
	seen    clockIdentity
	lacking *clockIdentity
}

func (p *appEnabledRoute) observe(base, uid, firmware string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = clockIdentity{base: base, uid: uid, firmware: firmware}
}

func (p *appEnabledRoute) currentLocked(base string) clockIdentity {
	if p.seen.base == base {
		return p.seen
	}
	return clockIdentity{base: base}
}

func (p *appEnabledRoute) worthTrying(base string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lacking == nil || *p.lacking != p.currentLocked(base)
}

func (p *appEnabledRoute) markLacking(base string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.currentLocked(base)
	p.lacking = &id
}

func validAppName(name string) bool {
	return appNamePattern.MatchString(name) && !reservedAppNames[name]
}

func (a *App) handleDeviceAppEnabledPut(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.startClockRotationOp(r)
	defer cancel()
	name := r.PathValue("name")
	if !validAppName(name) {
		writeError(w, http.StatusBadRequest, errAppNameInvalid)
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !a.decodeOrReject(w, r, &body, true) {
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, errAppEnabledMissing)
		return
	}
	release, ok := a.acquireClockRotation(ctx, w, r)
	if !ok {
		return
	}
	defer release()
	err := a.setClockAppEnabled(ctx, name, *body.Enabled)
	var apiErr *awtrix.APIError
	switch {
	case err == nil:
		a.setClockRotation(a.readBackClockRotation(ctx))
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, errAppIsModule):
		writeError(w, http.StatusBadRequest, err)
	case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusInsufficientStorage:
		a.setClockRotation(a.readBackClockRotation(ctx))
		writeDeviceAPIErrorStatus(w, apiErr, http.StatusInsufficientStorage)
	case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound:
		writeDeviceAPIErrorStatus(w, apiErr, http.StatusBadGateway)
	default:
		a.forgetClockRotationAfter(err)
		writeClockError(w, err)
	}
}

func (a *App) setClockAppEnabled(ctx context.Context, name string, on bool) error {
	if cached := a.clockRotation.Load(); cached != nil && listedAsModule(cached.listed, name) {
		return errAppIsModule
	}
	base := a.cfg.Load().effectiveClockURL()
	if a.appEnabled.worthTrying(base) {
		_, err := a.clock.fetch(ctx, func(cl *awtrix.Client, ctx context.Context) (awtrix.Reply, error) {
			return cl.RawPutAppEnabled(ctx, name, on)
		})
		if !clockLacksRoute(err) {
			return err
		}
		a.appEnabled.markLacking(base)
		a.logger.InfoContext(ctx, "clock has no per-app enabled route, writing the full app order",
			"base_url", base, "err", err)
	}
	body, err := a.clock.fetch(ctx, (*awtrix.Client).RawApps)
	if err != nil {
		return err
	}
	var rows []clockAppRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return fmt.Errorf("decode clock apps: %w", err)
	}
	order, err := appOrderWith(rows, name, on)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(order)
	_, err = a.clock.fetch(ctx, withBody((*awtrix.Client).RawPutAppOrder, payload))
	return err
}

func listedAsModule(listed []clockListedApp, name string) bool {
	for _, app := range listed {
		if app.Name == name && app.Origin == "module" {
			return true
		}
	}
	return false
}

func clockLacksRoute(err error) bool {
	var apiErr *awtrix.APIError
	return errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed)
}

type clockAppRow struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`
	Origin  string `json:"origin"`
	Slot    *int   `json:"slot"`
	Present *bool  `json:"present"`
}

func appOrderWith(rows []clockAppRow, name string, on bool) (deviceAppsPutBody, error) {
	out := deviceAppsPutBody{Order: []string{}, Disabled: []string{}}
	off := map[string]bool{}
	found := false
	for _, app := range rows {
		if app.Origin == "module" {
			if app.Name == name {
				return deviceAppsPutBody{}, errAppIsModule
			}
			continue
		}
		if app.Present == nil || *app.Present || app.Slot != nil {
			out.Order = append(out.Order, app.Name)
		}
		enabled := app.Enabled == nil || *app.Enabled
		if app.Name == name {
			enabled, found = on, true
		}
		if !enabled && !off[app.Name] {
			off[app.Name] = true
			out.Disabled = append(out.Disabled, app.Name)
		}
	}
	if !found && !on {
		out.Disabled = append(out.Disabled, name)
	}
	return out, nil
}
