package main

import (
	"strings"

	"github.com/tarakanof/ember/internal/sessions"
)

// knobLead is who the knob names under its face (cinder#42): the host that
// leads the winning state, how many hosts share that state, the lead's
// colour and tool. Comparable, so Upsert can tell when it moved.
type knobLead struct {
	Lead  string // "" when no session in the winning state has a source
	Hosts int    // distinct non-empty sources in the winning state
	Color string // first valid source_color of the lead's sessions, "" none
	Tool  string // the lead's tool when its sessions agree, else ""
}

// knobLeadOf picks the lead from the sessions in the winning state: the
// source with the most sessions, ties to the smaller name. Sources are
// compared case-insensitively (the knob uppercases them, so "m4" and "M4"
// are one host); the lead is returned uppercased. Unlike
// PickWinning's newest-wins this does not flip between hosts as their
// sessions heartbeat, so the view (and its long-poll) stays still.
func knobLeadOf(v sessions.View) knobLead {
	win := v.Winner()
	if win == nil {
		return knobLead{}
	}
	count := map[string]int{}
	for _, s := range v.Sessions {
		if s.State == win.State && s.Source != "" {
			count[strings.ToUpper(s.Source)]++
		}
	}
	var l knobLead
	for src, n := range count {
		if l.Lead == "" || n > count[l.Lead] || (n == count[l.Lead] && src < l.Lead) {
			l.Lead = src
		}
	}
	l.Hosts = len(count)
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
