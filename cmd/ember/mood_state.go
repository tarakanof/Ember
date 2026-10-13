package main

import (
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/sessions"
)

type moodState struct {
	Now      time.Time
	Sessions []Session
	Render   Render
	Lead     moodLead
}

func newMoodState(v sessions.View, idleText string) moodState {
	return moodState{Now: v.Now, Sessions: v.Sessions, Render: legacyRender(v, idleText), Lead: leadOf(v)}
}

func (a *App) moodOf(v sessions.View) moodState {
	return newMoodState(v, a.cfg.Load().Display.IdleText)
}

func (a *App) moodState() moodState { return a.moodOf(a.sessions.View()) }

func (s moodState) snapshot() Snapshot {
	return Snapshot{Now: s.Now, Sessions: s.Sessions, Render: s.Render}
}

type moodLead struct {
	Lead    string
	Hosts   int
	Color   string
	Tool    string
	Project string
}

func leadOf(v sessions.View) moodLead {
	win := v.Winner()
	if win == nil {
		return moodLead{}
	}
	count := map[string]int{}
	for _, s := range v.Sessions {
		if s.State == win.State && s.Source != "" {
			count[strings.ToUpper(s.Source)]++
		}
	}
	var l moodLead
	for src, n := range count {
		if l.Lead == "" || n > count[l.Lead] || (n == count[l.Lead] && src < l.Lead) {
			l.Lead = src
		}
	}
	l.Hosts = len(count)
	if win.State == "waiting" {
		return waitingLead(*win, l.Hosts)
	}
	if l.Lead == "" {
		return l
	}
	first := true
	for _, s := range v.Sessions {
		if s.State != win.State || strings.ToUpper(s.Source) != l.Lead {
			continue
		}
		if l.Color == "" && s.SourceColor != nil && isHexColor(*s.SourceColor) {
			l.Color = strings.ToUpper(*s.SourceColor)
		}
		if first {
			l.Tool, first = s.Tool, false
		} else if s.Tool != l.Tool {
			l.Tool = ""
		}
	}
	return l
}

func waitingLead(win Session, hosts int) moodLead {
	l := moodLead{Lead: strings.ToUpper(win.Source), Hosts: hosts, Tool: win.Tool, Project: win.Project}
	if l.Lead == "" {
		l.Hosts = 0
	}
	if win.SourceColor != nil && isHexColor(*win.SourceColor) {
		l.Color = strings.ToUpper(*win.SourceColor)
	}
	return l
}
