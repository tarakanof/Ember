package weather

import (
	"regexp"
	"time"
)

var zoneNamePattern = regexp.MustCompile(`^[A-Za-z]+(/[A-Za-z0-9_+\-]+){0,3}$`)

func loadZone(name string) *time.Location {
	if name == "Local" || !zoneNamePattern.MatchString(name) {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

type dayClock struct {
	loc *time.Location
	off time.Duration
}

func (c dayClock) dateOf(t time.Time) (int, time.Month, int) {
	if c.loc != nil {
		return t.In(c.loc).Date()
	}
	return t.UTC().Add(c.off).Date()
}

func (c dayClock) midnight(y int, m time.Month, d int) time.Time {
	if c.loc != nil {
		y, m, d = time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Date()
		t := time.Date(y, m, d, 0, 0, 0, 0, c.loc)
		if ty, tm, td := t.Date(); ty != y || tm != m || td != d {
			_, t = t.ZoneBounds()
		}
		return t.UTC()
	}
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-c.off)
}

func (c dayClock) startOf(t time.Time) time.Time { return c.midnight(c.dateOf(t)) }

func (c dayClock) after(start time.Time) time.Time {
	if c.loc == nil {
		return start.Add(24 * time.Hour)
	}
	y, m, d := c.dateOf(start)
	return c.midnight(y, m, d+1)
}

func DayEnd(d Day) time.Time {
	if !d.End.IsZero() {
		return d.End
	}
	return d.Start.Add(24 * time.Hour)
}
