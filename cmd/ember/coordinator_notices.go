package main

import (
	"context"
	"maps"
	"time"
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

type notice struct {
	app      string
	kind     noticeKind
	priority noticePriority
	sound    noticeSound
	payload  map[string]any
}

func (c *coordinator) quietAt(t time.Time) bool {
	enabled, start, end := c.loadCfg().quietHoursWindow()
	return enabled && quietActive(start, end, t)
}

func (c *coordinator) quietNow() bool { return c.quietAt(c.clk.Now()) }

func (c *coordinator) showNotice(ctx context.Context, n notice) error {
	p := make(map[string]any, len(n.payload)+5)
	maps.Copy(p, n.payload)
	p["name"] = noticeNames[n.kind]
	p["stack"] = n.priority == noticeQueue
	if !c.quietNow() {
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
	if err := c.publisher.Notify(ctx, p); err != nil {
		return err
	}
	c.logger.Debug("notice shown", "app", n.app, "name", p["name"])
	return nil
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
