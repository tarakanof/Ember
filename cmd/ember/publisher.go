package main

import (
	"context"

	"github.com/tarakanof/ember/internal/awtrix"
)

// Publisher is the seam for every server-initiated write to the clock: the coordinator's frames, tiles, indicators and display hold, and the one-shot notifications.
type Publisher interface {
	// CustomApp creates or replaces a pushed app, which is RAM-only on the device and vanishes on reboot.
	CustomApp(ctx context.Context, name string, payload map[string]any) error
	// ClearApp removes a pushed app.
	ClearApp(ctx context.Context, name string) error
	// ListApps returns the names of every app on the device, builtin and pushed alike.
	ListApps(ctx context.Context) ([]string, error)
	Notify(ctx context.Context, payload map[string]any) error
	// DismissNotifyByName clears the notification carrying name; a name the device no longer holds answers 404 (*awtrix.APIError), which callers treat as success.
	DismissNotifyByName(ctx context.Context, name string) error
	// PlayRTTTL plays an inline RTTTL melody and answers 503 when the clock has no buzzer.
	PlayRTTTL(ctx context.Context, rtttl string) error
	// Indicator lights one of the three corner LEDs.
	Indicator(ctx context.Context, index int, payload map[string]any) error
	// ClearIndicator turns a corner LED off.
	ClearIndicator(ctx context.Context, index int) error
	// Settings partially updates device settings, e.g. app rotation and native button navigation.
	Settings(ctx context.Context, payload map[string]any) error
	// ReadSettings returns the device settings resource, so a Pomodoro takeover can snapshot and later restore the user's own values.
	ReadSettings(ctx context.Context) (map[string]any, error)
	// Switch forces the device to the named app, with or without its transition animation.
	Switch(ctx context.Context, name string, mode awtrix.SwitchMode) error
	// ListIcons returns the filenames in the device's /ICONS folder.
	ListIcons(ctx context.Context) ([]string, error)
	// PutIcon uploads an icon file into the device's /ICONS folder.
	PutIcon(ctx context.Context, filename string, data []byte) error
}

// disabledPublisher is the Publisher under EMBER_CLOCK=off: every write
// succeeds without leaving the process.
type disabledPublisher struct{}

var _ Publisher = disabledPublisher{}

func (disabledPublisher) CustomApp(context.Context, string, map[string]any) error { return nil }
func (disabledPublisher) ClearApp(context.Context, string) error                  { return nil }
func (disabledPublisher) ListApps(context.Context) ([]string, error)              { return nil, nil }
func (disabledPublisher) Notify(context.Context, map[string]any) error            { return nil }
func (disabledPublisher) DismissNotifyByName(context.Context, string) error       { return nil }
func (disabledPublisher) PlayRTTTL(context.Context, string) error                 { return nil }
func (disabledPublisher) Indicator(context.Context, int, map[string]any) error    { return nil }
func (disabledPublisher) ClearIndicator(context.Context, int) error               { return nil }
func (disabledPublisher) Settings(context.Context, map[string]any) error          { return nil }
func (disabledPublisher) ReadSettings(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}
func (disabledPublisher) Switch(context.Context, string, awtrix.SwitchMode) error { return nil }
func (disabledPublisher) ListIcons(context.Context) ([]string, error)             { return nil, nil }
func (disabledPublisher) PutIcon(context.Context, string, []byte) error           { return nil }

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
