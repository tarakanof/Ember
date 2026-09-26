package main

import (
	"context"

	"github.com/tarakanof/ember/internal/awtrix"
)

// Publisher is the seam for every server-initiated write to the clock: the
// coordinator's frames, tiles, indicators and display hold, and the one-shot
// notifications. clockPublisher is the real adapter (below); tests pass a fake
// to NewApp. Sound policy (quietPublisher) and retries (coordinator.
// retryDevice) sit above the seam, so a fake sees what the clock would.
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

// clockPublisher is the real Publisher: every method is one callPublish
// client call through clockAccess against the currently-resolved clock (a
// fresh client per call reads the live URL, so a rediscovery swap applies to
// the next write). It is ungated: NewApp builds the only one and wraps it in
// quietPublisher at once, so nothing else can reach Notify or PlayRTTTL
// without the quiet-hours check (clock_access_guard_test.go keeps it that
// way). Tests substitute a fake at the same seam.
type clockPublisher struct{ k *clockAccess }

var _ Publisher = clockPublisher{}

func (p clockPublisher) publish(ctx context.Context, fn func(context.Context, *awtrix.Client) error) error {
	return p.k.do(ctx, callPublish, fn)
}

func (p clockPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.PushApp(ctx, name, payload) })
}

func (p clockPublisher) ClearApp(ctx context.Context, name string) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.DeleteApp(ctx, name) })
}

func (p clockPublisher) ListApps(ctx context.Context) ([]string, error) {
	var names []string
	err := p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error {
		apps, err := c.ListApps(ctx)
		if err != nil {
			return err
		}
		names = make([]string, 0, len(apps))
		for _, a := range apps {
			names = append(names, a.Name)
		}
		return nil
	})
	return names, err
}

func (p clockPublisher) ListIcons(ctx context.Context) ([]string, error) {
	var names []string
	err := p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error {
		var err error
		names, err = c.ListIcons(ctx)
		return err
	})
	return names, err
}

func (p clockPublisher) PutIcon(ctx context.Context, filename string, data []byte) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.PutIcon(ctx, filename, data) })
}

func (p clockPublisher) Notify(ctx context.Context, payload map[string]any) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.Notify(ctx, payload) })
}

func (p clockPublisher) DismissNotifyByName(ctx context.Context, name string) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.DismissNotifyByName(ctx, name) })
}

func (p clockPublisher) PlayRTTTL(ctx context.Context, rtttl string) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.PlayRTTTL(ctx, rtttl) })
}

func (p clockPublisher) Indicator(ctx context.Context, index int, payload map[string]any) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.SetIndicator(ctx, index, payload) })
}

func (p clockPublisher) ClearIndicator(ctx context.Context, index int) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.ClearIndicator(ctx, index) })
}

func (p clockPublisher) Settings(ctx context.Context, payload map[string]any) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.PatchSettings(ctx, payload) })
}

func (p clockPublisher) ReadSettings(ctx context.Context) (map[string]any, error) {
	var m map[string]any
	err := p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error {
		var err error
		m, err = c.GetSettings(ctx)
		return err
	})
	return m, err
}

func (p clockPublisher) Switch(ctx context.Context, name string, mode awtrix.SwitchMode) error {
	return p.publish(ctx, func(ctx context.Context, c *awtrix.Client) error { return c.SwitchApp(ctx, name, mode) })
}
