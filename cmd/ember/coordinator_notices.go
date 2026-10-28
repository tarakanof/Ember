package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

type noticeKind int

const (
	noticeWeather noticeKind = iota
	noticeAir
	noticeSun
	noticeReminder
	noticeMeeting
	noticePomodoro
	noticeMessage
	noticeUsageReset
)

var noticeNames = map[noticeKind]string{
	noticeWeather:    notifyNameWeatherPopup,
	noticeAir:        notifyNameAirPopup,
	noticeSun:        notifyNameSunPopup,
	noticeReminder:   notifyNameReminder,
	noticeMeeting:    notifyNameMeeting,
	noticePomodoro:   notifyNamePomodoro,
	noticeMessage:    notifyNameNotify,
	noticeUsageReset: notifyNameUsageAlarm,
}

type noticePriority int

const (
	noticeInterrupt noticePriority = iota
	noticeQueue
)

type noticeSound struct {
	melody string
	rtttl  string
	loop   bool
}

var soundNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func validSoundSetting(field, v string) error {
	if v == "" {
		return nil
	}
	name, _, rtttl := strings.Cut(v, ":")
	switch {
	case !rtttl && !soundNamePattern.MatchString(v):
		return fmt.Errorf("%s %q must be a stored melody name (1-32 of A-Z a-z 0-9 _ -) or RTTTL text, as awtrix-ng 1.2 plays", field, v)
	case rtttl && (name == "" || len(name) > 24 || strings.Count(v, ":") != 2 || len(v) > 512 || !printableASCII.MatchString(v)):
		return fmt.Errorf("%s must be RTTTL text as awtrix-ng 1.2 plays it: name:defaults:notes, a 1-24 character name, up to 512 characters", field)
	}
	return nil
}

func soundSetting(v string) noticeSound {
	if strings.Contains(v, ":") {
		return noticeSound{rtttl: v}
	}
	return noticeSound{melody: v}
}

func (s noticeSound) object() any {
	var src map[string]any
	switch {
	case s.melody != "" && !s.loop:
		return s.melody
	case s.melody != "":
		src = map[string]any{"file": s.melody}
	case s.rtttl != "":
		src = map[string]any{"rtttl": s.rtttl}
	default:
		return nil
	}
	if s.loop {
		src["loop"] = true
	}
	return src
}

type notice struct {
	app      string
	kind     noticeKind
	priority noticePriority
	sound    noticeSound
	payload  map[string]any
}

func (c *coordinator) quietAt(t time.Time) bool {
	if c.quiet != nil {
		return c.quiet(t)
	}
	return c.loadCfg().quietAt(t)
}

func (c *coordinator) quietNow() bool { return c.quietAt(c.clk.Now()) }

func (c *coordinator) showNotice(ctx context.Context, n notice) error {
	return c.showNoticeQuiet(ctx, n, c.quietNow())
}

func (c *coordinator) showNoticeQuiet(ctx context.Context, n notice, quiet bool) error {
	object := c.useSoundObject()
	p := noticePayload(n, quiet, object)
	err := c.publisher.Notify(ctx, p)
	if err != nil && !quiet && n.sound != (noticeSound{}) && isUnprocessable(err) && c.refreshSound != nil {
		c.refreshSound(ctx)
		if again := c.useSoundObject(); again != object {
			c.logger.Info("notice sound refused; retrying in the clock's audio format", "app", n.app, "sound_object", again)
			p = noticePayload(n, quiet, again)
			err = c.publisher.Notify(ctx, p)
		}
		if err != nil && isSoundRefusal(err) {
			c.logger.Warn("notice sound refused; showing it without sound", "app", n.app, "err", err)
			p = noticePayload(n, true, false)
			err = c.publisher.Notify(ctx, p)
		}
	}
	if err != nil {
		return err
	}
	c.logger.Debug("notice shown", "app", n.app, "name", p["name"])
	return nil
}

func (c *coordinator) useSoundObject() bool {
	return c.soundObject != nil && c.soundObject()
}

func isUnprocessable(err error) bool {
	var apiErr *awtrix.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity
}

func isSoundRefusal(err error) bool {
	var apiErr *awtrix.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity &&
		strings.HasPrefix(apiErr.Field, "sound")
}

func noticePayload(n notice, quiet, object bool) map[string]any {
	p := make(map[string]any, len(n.payload)+5)
	maps.Copy(p, n.payload)
	p["name"] = noticeNames[n.kind]
	p["stack"] = n.priority == noticeQueue
	switch {
	case quiet:
	case object:
		if v := n.sound.object(); v != nil {
			p["sound"] = v
		}
	default:
		if n.sound.melody != "" {
			p["sound"] = n.sound.melody
		}
		if n.sound.rtttl != "" {
			p["soundRtttl"] = n.sound.rtttl
		}
		if n.sound.loop {
			p["soundLoop"] = true
		}
	}
	return p
}

func (c *coordinator) dismissNotice(ctx context.Context, kind noticeKind) error {
	return c.publisher.DismissNotifyByName(ctx, noticeNames[kind])
}

func (c *coordinator) playChime(ctx context.Context, rtttl string) error {
	if c.quietNow() {
		return nil
	}
	return c.publisher.PlayRTTTL(ctx, rtttl)
}
