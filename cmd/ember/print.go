package main

import (
	"context"
	"fmt"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
	"github.com/tarakanof/ember/internal/printer"
)

const (
	printerSettingsKey   = "printer_json"
	printerBrowseTimeout = 3 * time.Second
)

func (a *App) newPrinter(browse printer.BrowseFunc) *printer.Poller {
	p := printer.NewPoller(printer.Options{
		Config: func() printer.Config { return a.cfg.Load().Printer },
		Browse: browse,
		Logger: a.logger,
	})
	p.OnChange = func() { a.changes.notify(topicPrint) }
	return p
}

func browsePrusaLink(ctx context.Context) ([]printer.Found, error) {
	found, err := discovery.BrowsePrusaLink(ctx, printerBrowseTimeout)
	if err != nil {
		return nil, err
	}
	out := make([]printer.Found, 0, len(found))
	for _, f := range found {
		out = append(out, printer.Found{Name: f.Name, Host: f.Host, URL: f.URL})
	}
	return out, nil
}

func (a *App) configurePrinter(creds printer.Credentials) {
	a.printer.SetCredentials(creds)
	cfg := a.cfg.Load().Printer
	if !cfg.Enabled && creds.Password == "" {
		return
	}
	a.logger.Info("printer", "enabled", cfg.Enabled, "url", cfg.URL, "name", cfg.Name, "prusalink", creds)
}

func (a *App) printerSettingSpec() settingSpec[printer.Config] {
	return settingSpec[printer.Config]{
		key:  printerSettingsKey,
		view: func(c Config) printer.Config { return c.Printer },
		apply: func(c *Config, p printer.Config) error {
			p.ApplyDefaults()
			if err := p.Validate(); err != nil {
				return err
			}
			c.Printer = p
			return nil
		},
		after: func(Config) { a.printer.Wake() },
	}
}

func checkPrinter(app *App) CheckResult {
	s := app.printer.State.At(time.Now())
	res := CheckResult{BaseURL: s.Printer.URL}
	if s.Printer.URL != "" {
		res.Source = "config"
		if s.Printer.Discovered {
			res.Source = "mdns"
		}
	}
	switch {
	case !s.Enabled:
		res.Status, res.Detail = StatusOK, "disabled (printer.enabled)"
	case s.LastError == printer.ErrCodeNoPassword:
		res.Status, res.Detail = StatusWarn, "password missing: set "+printer.PasswordEnv
	case s.LastError == printer.ErrCodeWrongPassword:
		res.Status, res.Detail = StatusWarn, "wrong password: check "+printer.PasswordEnv+" against the printer (Settings → Network → PrusaLink)"
	case s.LastError == printer.ErrCodeNotFound:
		res.Status, res.Detail = StatusWarn, "no _prusalink._tcp printer on the network; set printer.url"
	case s.LastError == printer.ErrCodeUnreachable:
		res.Status, res.Detail = StatusWarn, "unreachable (switched off?)"
	case s.UpdatedAt.IsZero():
		res.Status, res.Detail = StatusOK, "enabled; not polled yet"
	default:
		res.Status = StatusOK
		res.Detail = fmt.Sprintf("phase=%s firmware=%s hostname=%s last_poll=%s ago", s.Phase, s.Printer.Firmware,
			s.Printer.Hostname, time.Since(s.UpdatedAt).Round(time.Second))
	}
	return res
}
