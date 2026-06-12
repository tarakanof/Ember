// Package meetings parses ICS calendar feeds and expands recurring events into
// concrete occurrences. Pure functions, no I/O — the server's poller feeds it
// fetched bytes. Recurrence (RRULE/EXDATE/RECURRENCE-ID) is delegated to
// rrule-go; ICS tokenizing (folding, escaping) to golang-ical; DTSTART/EXDATE
// time parsing is ours (TZID + UTC + floating forms).
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

// Occurrence is a single concrete instance of a calendar event.
type Occurrence struct {
	UID   string
	Title string
	Start time.Time
	End   time.Time
}

// Expand parses an ICS calendar from data, expands all recurring events, and
// returns occurrences whose Start falls in [from, from+horizon). All-day
// events and cancelled events are skipped. EXDATE exclusions and RECURRENCE-ID
// overrides are applied.
func Expand(data []byte, from time.Time, horizon time.Duration) ([]Occurrence, error) {
	cal, err := ics.ParseCalendar(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse calendar: %w", err)
	}

	until := from.Add(horizon)
	events := cal.Events()

	// Separate master events from override events (those with RECURRENCE-ID).
	type overrideKey struct{ uid, recurrID string }
	overrides := make(map[overrideKey]*ics.VEvent)
	var masters []*ics.VEvent

	for _, e := range events {
		if e.GetProperty(ics.ComponentPropertyRecurrenceId) != nil {
			uid := eventUID(e)
			ridProp := e.GetProperty(ics.ComponentPropertyRecurrenceId)
			ridTime, _, ridErr := parseICSTime(ridProp.Value, ridProp.ICalParameters)
			if ridErr != nil {
				// Malformed override — skip it.
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

		// Skip cancelled masters.
		if statusProp := e.GetProperty(ics.ComponentPropertyStatus); statusProp != nil {
			if strings.EqualFold(statusProp.Value, string(ics.ObjectStatusCancelled)) {
				continue
			}
		}

		// Parse DTSTART.
		startProp := e.GetProperty(ics.ComponentPropertyDtStart)
		if startProp == nil {
			continue
		}
		masterStart, allDay, err := parseICSTime(startProp.Value, startProp.ICalParameters)
		if err != nil || allDay {
			continue // skip all-day and unparseable events
		}

		// Parse DTEND (absent → same as start).
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
			// Non-recurring: keep if in window.
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

		// Recurring event: expand with rrule-go.
		r, err := rrule.StrToRRule(rruleProp.Value)
		if err != nil {
			continue // invalid RRULE — skip the event
		}
		// DTStart must carry the original location so rrule-go iterates in that
		// zone — this is what makes the DST test pass (09:00 CET → 09:00 CEST).
		r.DTStart(masterStart)

		// Build EXDATE set (UTC RFC3339 keys).
		exdates, exErr := e.GetExDates()
		if exErr != nil {
			// Non-fatal: proceed without exdates.
			exdates = nil
		}
		exSet := make(map[string]bool, len(exdates))
		for _, ex := range exdates {
			exSet[ex.UTC().Format(time.RFC3339)] = true
		}

		// Between is inclusive on both ends when inc=true; we want [from, until).
		// Pass inc=true, then filter out the exact boundary.
		instances := r.Between(from, until, true)
		for _, inst := range instances {
			if !inst.Before(until) {
				continue // exclude exact upper bound
			}
			instKey := inst.UTC().Format(time.RFC3339)
			if exSet[instKey] {
				continue // EXDATE'd
			}
			ovKey := overrideKey{uid: uid, recurrID: instKey}
			if ov, ok := overrides[ovKey]; ok {
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
	}

	// Overrides whose master is not in the feed are treated as plain single events.
	masterUIDs := make(map[string]bool, len(masters))
	for _, e := range masters {
		masterUIDs[eventUID(e)] = true
	}
	for _, ov := range overrides {
		uid := eventUID(ov)
		if masterUIDs[uid] {
			continue // already handled above
		}
		startProp := ov.GetProperty(ics.ComponentPropertyDtStart)
		if startProp == nil {
			continue
		}
		ovStart, allDay, err := parseICSTime(startProp.Value, startProp.ICalParameters)
		if err != nil || allDay {
			continue
		}
		if ovStart.Before(from) || !ovStart.Before(until) {
			continue
		}
		ovEnd := ovStart
		if endProp := ov.GetProperty(ics.ComponentPropertyDtEnd); endProp != nil {
			t, ad, err := parseICSTime(endProp.Value, endProp.ICalParameters)
			if err == nil && !ad {
				ovEnd = t
			}
		}
		result = append(result, Occurrence{
			UID:   uid,
			Title: unescapeText(eventSummary(ov)),
			Start: ovStart,
			End:   ovEnd,
		})
	}

	sortOccurrences(result)
	return result, nil
}

// applyOverride converts a RECURRENCE-ID override event into an Occurrence,
// returning (occ, true) when the override's new Start is in [from, until) and
// the override is not cancelled or all-day.
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

// Merge concatenates any number of occurrence lists, sorts by Start then
// Title, and deduplicates on UID + Start (UTC, RFC3339 precision).
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

	// Deduplicate: keep first occurrence of each (uid, start-UTC) pair.
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

// parseICSTime parses an ICS date-time property value. allDay is true for
// VALUE=DATE (or a bare yyyymmdd value); callers skip those events. A trailing
// Z is UTC; a TZID parameter resolves via time.LoadLocation (the binary embeds
// the tz database — see the time/tzdata import in cmd/ember, added in a later
// task); a floating time falls back to server-local (UTC in the container —
// documented limitation).
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

// unescapeText reverses ICS text escaping (RFC 5545 §3.3.11).
// It processes escape sequences left-to-right in a single pass so that \\n
// correctly yields a backslash followed by the letter n (not `\` + space).
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
				// Unknown escape: emit as-is.
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

// eventUID returns the UID of a VEVENT, or empty string if absent.
func eventUID(e *ics.VEvent) string {
	p := e.GetProperty(ics.ComponentPropertyUniqueId)
	if p == nil {
		return ""
	}
	return p.Value
}

// eventSummary returns the raw SUMMARY value, or empty string if absent.
func eventSummary(e *ics.VEvent) string {
	p := e.GetProperty(ics.ComponentPropertySummary)
	if p == nil {
		return ""
	}
	return p.Value
}

// sortOccurrences sorts in-place by Start ascending, then Title ascending.
func sortOccurrences(occs []Occurrence) {
	slices.SortFunc(occs, func(a, b Occurrence) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return strings.Compare(a.Title, b.Title)
	})
}
