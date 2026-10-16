package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/tarakanof/ember/internal/awtrix"
)

var errAppEnabledMissing = errors.New("enabled must be true or false")

func (a *App) handleDeviceAppEnabledPut(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.startClockRotationOp(r)
	defer cancel()
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
	if err := a.setClockAppEnabled(ctx, r.PathValue("name"), *body.Enabled); err != nil {
		a.forgetClockRotationAfter(err)
		writeClockError(w, err)
		return
	}
	a.setClockRotation(a.readBackClockRotation(ctx))
	w.WriteHeader(http.StatusOK)
}

func (a *App) setClockAppEnabled(ctx context.Context, name string, on bool) error {
	if fw := a.deviceFirmware(); clockHasAppEnabled(fw) {
		_, err := a.clock.fetch(ctx, func(cl *awtrix.Client, ctx context.Context) (awtrix.Reply, error) {
			return cl.RawPutAppEnabled(ctx, name, on)
		})
		if !clockLacksRoute(err) {
			return err
		}
		a.logger.InfoContext(ctx, "clock refused the per-app enabled route, writing the full app order",
			"firmware", fw, "err", err)
	}
	body, err := a.clock.fetch(ctx, (*awtrix.Client).RawApps)
	if err != nil {
		return err
	}
	var listed []clockListedApp
	if err := json.Unmarshal(body, &listed); err != nil {
		return fmt.Errorf("decode clock apps: %w", err)
	}
	payload, _ := json.Marshal(appOrderWith(listed, name, on))
	_, err = a.clock.fetch(ctx, withBody((*awtrix.Client).RawPutAppOrder, payload))
	return err
}

func clockLacksRoute(err error) bool {
	var apiErr *awtrix.APIError
	return errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed)
}

func appOrderWith(listed []clockListedApp, name string, on bool) deviceAppsPutBody {
	out := deviceAppsPutBody{Order: []string{}, Disabled: []string{}}
	off := map[string]bool{}
	found := false
	for _, app := range listed {
		if app.Origin == "module" {
			continue
		}
		if app.Present == nil || *app.Present || app.Slot != nil {
			out.Order = append(out.Order, app.Name)
		}
		enabled := app.Enabled
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
	return out
}
