package main

import (
	"context"

	"github.com/tarakanof/ember/internal/awtrix"
)

type Publisher interface {
	// CustomApp creates or replaces a pushed app
	// (PUT /api/v1/apps/pushed/{name}). Pushed apps are RAM-only on awtrix-ng
	// and vanish on device reboot.
	CustomApp(ctx context.Context, name string, payload map[string]any) error
	// ClearApp removes a pushed app (DELETE /api/v1/apps/{name}). Used by the
	// usage-app reconcile to drop stale/hidden apps.
	ClearApp(ctx context.Context, name string) error
	// ListApps returns the names of every app on the device
	// (GET /api/v1/apps), builtin and pushed alike. Used on startup to adopt
	// ember-managed pushed apps left from a previous run so they can be
	// reconciled/cleared even though the in-memory push trackers start empty.
	ListApps(ctx context.Context) ([]string, error)
	Notify(ctx context.Context, payload map[string]any) error
	// DismissNotifyByName clears the notification carrying name
	// (DELETE /api/v1/notifications/{name}). Used to acknowledge Ember's own
	// held reminder alarm when the user presses a clock button, so a foreign
	// notification can never be the one that gets cleared. A name the device
	// no longer holds answers 404 (*awtrix.APIError).
	DismissNotifyByName(ctx context.Context, name string) error
	// PlayRTTTL plays an inline RTTTL melody (POST /api/v1/audio/play). Only
	// for chimes with no notification of their own — the attention-lock chime.
	// Popups carry their melody on the notification's soundRtttl key instead:
	// awtrix-ng plays it alongside draw/icon, unlike AWTRIX3.
	PlayRTTTL(ctx context.Context, rtttl string) error
	// Indicator lights one of the three corner LEDs
	// (PUT /api/v1/indicators/{1-3}).
	Indicator(ctx context.Context, index int, payload map[string]any) error
	// ClearIndicator turns a corner LED off (DELETE /api/v1/indicators/{1-3}).
	ClearIndicator(ctx context.Context, index int) error
	// Settings partially updates device settings (PATCH /api/v1/settings),
	// e.g. toggling app rotation and native button navigation for Pomodoro
	// takeover.
	Settings(ctx context.Context, payload map[string]any) error
	// ReadSettings returns the device settings resource (GET /api/v1/settings).
	// Used to snapshot the user's own values before a Pomodoro takeover
	// overrides them, so the restore puts back what was there.
	ReadSettings(ctx context.Context) (map[string]any, error)
	// Switch forces the device to the named app (PUT /api/v1/apps/active),
	// with or without the device's transition animation.
	Switch(ctx context.Context, name string, mode awtrix.SwitchMode) error
	// ListIcons returns the filenames in the device's /ICONS folder
	// (GET /api/v1/files?dir=/ICONS). Used by the weather icon provisioner to
	// find missing gallery icons.
	ListIcons(ctx context.Context) ([]string, error)
	// PutIcon uploads an icon file into /ICONS
	// (multipart POST /api/v1/files?dir=/ICONS). The AWTRIX3 firmware's own
	// on-demand gallery downloads were unreliable, so the server provisions
	// icons itself.
	PutIcon(ctx context.Context, filename string, data []byte) error
}

// HTTPPublisher drives the clock over awtrix-ng's API v1, delegating every
// call to internal/awtrix. A fresh client is built per call from the live
// config so URL changes from rediscovery take effect immediately.
type HTTPPublisher struct {
	app *App // for reading current AWTRIX config
}

// NewHTTPPublisher returns a publisher with no app reference yet. Callers
// must set p.app before calling any publish method (NewApp does this).
func NewHTTPPublisher() (*HTTPPublisher, error) {
	return &HTTPPublisher{}, nil
}

func (p *HTTPPublisher) client() (*awtrix.Client, error) {
	return p.app.clock.client(callPublish)
}

func (p *HTTPPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PushApp(ctx, name, payload)
}

func (p *HTTPPublisher) ClearApp(ctx context.Context, name string) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.DeleteApp(ctx, name)
}

func (p *HTTPPublisher) ListApps(ctx context.Context) ([]string, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	apps, err := c.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Name)
	}
	return names, nil
}

func (p *HTTPPublisher) ListIcons(ctx context.Context) ([]string, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return c.ListIcons(ctx)
}

func (p *HTTPPublisher) PutIcon(ctx context.Context, filename string, data []byte) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PutIcon(ctx, filename, data)
}

func (p *HTTPPublisher) Notify(ctx context.Context, payload map[string]any) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.Notify(ctx, payload)
}

func (p *HTTPPublisher) DismissNotifyByName(ctx context.Context, name string) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.DismissNotifyByName(ctx, name)
}

func (p *HTTPPublisher) PlayRTTTL(ctx context.Context, rtttl string) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PlayRTTTL(ctx, rtttl)
}

func (p *HTTPPublisher) Indicator(ctx context.Context, index int, payload map[string]any) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.SetIndicator(ctx, index, payload)
}

func (p *HTTPPublisher) ClearIndicator(ctx context.Context, index int) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.ClearIndicator(ctx, index)
}

func (p *HTTPPublisher) Settings(ctx context.Context, payload map[string]any) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PatchSettings(ctx, payload)
}

func (p *HTTPPublisher) ReadSettings(ctx context.Context) (map[string]any, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return c.GetSettings(ctx)
}

func (p *HTTPPublisher) Switch(ctx context.Context, name string, mode awtrix.SwitchMode) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.SwitchApp(ctx, name, mode)
}
