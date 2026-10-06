package main

import (
	"strings"

	"github.com/tarakanof/ember/internal/sessions"
)

type knobLead struct {
	Lead  string
	Hosts int
	Color string
	Tool  string
}

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
