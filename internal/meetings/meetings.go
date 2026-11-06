package meetings

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

type Occurrence struct {
	UID   string
	Title string
	Start time.Time
	End   time.Time
}

func Expand(data []byte, from time.Time, horizon time.Duration) ([]Occurrence, error) {
	cal, err := ics.ParseCalendar(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse calendar: %w", err)
	}

	until := from.Add(horizon)
	events := cal.Events()

	type overrideKey struct{ uid, recurrID string }
	overrides := make(map[overrideKey]*ics.VEvent)
	var masters []*ics.VEvent

	for _, e := range events {
		if e.GetProperty(ics.ComponentPropertyRecurrenceId) != nil {
			uid := eventUID(e)
			ridProp := e.GetProperty(ics.ComponentPropertyRecurrenceId)
			ridTime, _, ridErr := parseICSTime(ridProp.Value, ridProp.ICalParameters)
			if ridErr != nil {
				continue
			}
			key := overrideKey{
				uid:      uid,
				recurrID: ridTime.UTC().Format(time.RFC3339),
			}
			overrides[key] = e
		} else {
			masters = append(masters, e)
		}
	}

	var result []Occurrence

	for _, e := range masters {
		uid := eventUID(e)

		if statusProp := e.GetProperty(ics.ComponentPropertyStatus); statusProp != nil {
			if strings.EqualFold(statusProp.Value, string(ics.ObjectStatusCancelled)) {
				continue
			}
		}

		startProp := e.GetProperty(ics.ComponentPropertyDtStart)
		if startProp == nil {
			continue
		}
		masterStart, allDay, err := parseICSTime(startProp.Value, startProp.ICalParameters)
		if err != nil || allDay {
			continue
		}

		masterEnd := masterStart
		if endProp := e.GetProperty(ics.ComponentPropertyDtEnd); endProp != nil {
			t, ad, err := parseICSTime(endProp.Value, endProp.ICalParameters)
			if err == nil && !ad {
				masterEnd = t
			}
		}
		duration := masterEnd.Sub(masterStart)

		title := unescapeText(eventSummary(e))

		rruleProp := e.GetProperty(ics.ComponentPropertyRrule)

		if rruleProp == nil {
			if masterStart.Before(from) || !masterStart.Before(until) {
				continue
			}
			key := overrideKey{uid: uid, recurrID: masterStart.UTC().Format(time.RFC3339)}
			if ov, ok := overrides[key]; ok {
				occ, keep := applyOverride(ov, uid, from, until)
				if keep {
					result = append(result, occ)
				}
				continue
			}
			result = append(result, Occurrence{
				UID:   uid,
				Title: title,
				Start: masterStart,
				End:   masterEnd,
			})
			continue
		}

		r, err := rrule.StrToRRule(rruleProp.Value)
		if err != nil {
			continue
		}
		r.DTStart(masterStart)

		exdates, exErr := e.GetExDates()
		if exErr != nil {
			exdates = nil
		}
		exSet := make(map[string]bool, len(exdates))
		for _, ex := range exdates {
			exSet[ex.UTC().Format(time.RFC3339)] = true
		}

		instances := r.Between(from, until, true)
		consumedOvKeys := make(map[overrideKey]bool)
		for _, inst := range instances {
			if !inst.Before(until) {
				continue
			}
			instKey := inst.UTC().Format(time.RFC3339)
			if exSet[instKey] {
				continue
			}
			ovKey := overrideKey{uid: uid, recurrID: instKey}
			if ov, ok := overrides[ovKey]; ok {
				consumedOvKeys[ovKey] = true
				occ, keep := applyOverride(ov, uid, from, until)
				if keep {
					result = append(result, occ)
				}
				continue
			}
			instEnd := inst.Add(duration)
			result = append(result, Occurrence{
				UID:   uid,
				Title: title,
				Start: inst,
				End:   instEnd,
			})
		}

		for ovKey, ov := range overrides {
			if ovKey.uid != uid {
				continue
			}
			if consumedOvKeys[ovKey] {
				continue
			}
			occ, keep := applyOverride(ov, uid, from, until)
			if keep {
				result = append(result, occ)
			}
		}
	}

	masterUIDs := make(map[string]bool, len(masters))
	for _, e := range masters {
		masterUIDs[eventUID(e)] = true
	}
	for key, ov := range overrides {
		if masterUIDs[key.uid] {
			continue
		}
		occ, keep := applyOverride(ov, key.uid, from, until)
		if keep {
			result = append(result, occ)
		}
	}

	sortOccurrences(result)
	return result, nil
}

func applyOverride(ov *ics.VEvent, uid string, from, until time.Time) (Occurrence, bool) {
	if statusProp := ov.GetProperty(ics.ComponentPropertyStatus); statusProp != nil {
		if strings.EqualFold(statusProp.Value, string(ics.ObjectStatusCancelled)) {
			return Occurrence{}, false
		}
	}
	startProp := ov.GetProperty(ics.ComponentPropertyDtStart)
	if startProp == nil {
		return Occurrence{}, false
	}
	ovStart, allDay, err := parseICSTime(startProp.Value, startProp.ICalParameters)
	if err != nil || allDay {
		return Occurrence{}, false
	}
	if ovStart.Before(from) || !ovStart.Before(until) {
		return Occurrence{}, false
	}
	ovEnd := ovStart
	if endProp := ov.GetProperty(ics.ComponentPropertyDtEnd); endProp != nil {
		t, ad, err := parseICSTime(endProp.Value, endProp.ICalParameters)
		if err == nil && !ad {
			ovEnd = t
		}
	}
	return Occurrence{
		UID:   uid,
		Title: unescapeText(eventSummary(ov)),
		Start: ovStart,
		End:   ovEnd,
	}, true
}

func Merge(lists ...[]Occurrence) []Occurrence {
	total := 0
	for _, l := range lists {
		total += len(l)
	}
	result := make([]Occurrence, 0, total)
	for _, l := range lists {
		result = append(result, l...)
	}
	sortOccurrences(result)

	seen := make(map[string]bool, len(result))
	deduped := result[:0]
	for _, occ := range result {
		key := occ.UID + "|" + occ.Start.UTC().Format(time.RFC3339)
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, occ)
	}
	return deduped
}

func parseICSTime(value string, params map[string][]string) (t time.Time, allDay bool, err error) {
	if (len(params["VALUE"]) > 0 && params["VALUE"][0] == "DATE") || len(value) == 8 {
		t, err = time.Parse("20060102", value)
		return t, true, err
	}
	if strings.HasSuffix(value, "Z") {
		t, err = time.ParseInLocation("20060102T150405Z", value, time.UTC)
		return t, false, err
	}
	loc := time.Local
	if tz := params["TZID"]; len(tz) > 0 {
		if l, lerr := time.LoadLocation(tz[0]); lerr == nil {
			loc = l
		}
	}
	t, err = time.ParseInLocation("20060102T150405", value, loc)
	return t, false, err
}

func unescapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case ',':
				b.WriteByte(',')
			case ';':
				b.WriteByte(';')
			case 'n', 'N':
				b.WriteByte(' ')
			default:
				b.WriteByte(s[i])
				b.WriteByte(s[i+1])
			}
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func eventUID(e *ics.VEvent) string {
	p := e.GetProperty(ics.ComponentPropertyUniqueId)
	if p == nil {
		return ""
	}
	return p.Value
}

func eventSummary(e *ics.VEvent) string {
	p := e.GetProperty(ics.ComponentPropertySummary)
	if p == nil {
		return ""
	}
	return p.Value
}

func sortOccurrences(occs []Occurrence) {
	slices.SortFunc(occs, func(a, b Occurrence) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		if c := strings.Compare(a.Title, b.Title); c != 0 {
			return c
		}
		return strings.Compare(a.UID, b.UID)
	})
}
