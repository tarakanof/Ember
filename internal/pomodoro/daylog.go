package pomodoro

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/tarakanof/ember/internal/workday"
)

func (s *Store) RecordDayEvent(e workday.Event) error {
	_, err := s.db.Exec(`INSERT INTO day_events (at, kind, value) VALUES (?, ?, ?)`,
		e.At.Unix(), string(e.Kind), e.Value)
	if err != nil {
		return fmt.Errorf("record day event: %w", err)
	}
	return nil
}

func (s *Store) LastDayEvents(n int) ([]workday.Event, error) {
	out, err := s.queryDayEvents(`SELECT at, kind, value FROM day_events ORDER BY at DESC, id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	slices.Reverse(out)
	return out, nil
}

func (s *Store) DayEventsBetween(lo, hi time.Time) ([]workday.Event, error) {
	return s.queryDayEvents(`SELECT at, kind, value FROM day_events WHERE at >= ? AND at < ? ORDER BY at, id`,
		lo.Unix(), hi.Unix())
}

func (s *Store) queryDayEvents(query string, args ...any) ([]workday.Event, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("day events: %w", err)
	}
	defer rows.Close()
	var out []workday.Event
	for rows.Next() {
		var (
			at          int64
			kind, value string
		)
		if err := rows.Scan(&at, &kind, &value); err != nil {
			return nil, fmt.Errorf("scan day event: %w", err)
		}
		out = append(out, workday.Event{At: time.Unix(at, 0), Kind: workday.Kind(kind), Value: value})
	}
	return out, rows.Err()
}

func (s *Store) DayEventsFromLastStart() (events []workday.Event, started bool, err error) {
	var at, id int64
	err = s.db.QueryRow(`SELECT at, id FROM day_events WHERE kind = ? ORDER BY at DESC, id DESC LIMIT 1`,
		string(workday.Start)).Scan(&at, &id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		events, err = s.LastDayEvents(1)
		return events, false, err
	case err != nil:
		return nil, false, fmt.Errorf("last day start: %w", err)
	}
	events, err = s.queryDayEvents(
		`SELECT at, kind, value FROM day_events WHERE at > ? OR (at = ? AND id >= ?) ORDER BY at, id`, at, at, id)
	return events, true, err
}

func (s *Store) RecordDayReply(r workday.Reply) error {
	_, err := s.db.Exec(`INSERT INTO day_replies (at, source, tool, session_key) VALUES (?, ?, ?, ?)`,
		r.At.Unix(), r.Source, r.Tool, r.SessionKey)
	if err != nil {
		return fmt.Errorf("record day reply: %w", err)
	}
	return nil
}

func (s *Store) LastDayReply() (time.Time, bool, error) {
	var at sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(at) FROM day_replies`).Scan(&at); err != nil {
		return time.Time{}, false, fmt.Errorf("last day reply: %w", err)
	}
	if !at.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(at.Int64, 0), true, nil
}

func (s *Store) CountDayReplies(since time.Time) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM day_replies WHERE at >= ?`, since.Unix()).Scan(&n); err != nil {
		return 0, fmt.Errorf("count day replies: %w", err)
	}
	return n, nil
}

func (s *Store) DayRepliesBetween(lo, hi time.Time) ([]workday.Reply, error) {
	rows, err := s.db.Query(
		`SELECT at, source, tool, session_key FROM day_replies WHERE at >= ? AND at < ? ORDER BY at, id`,
		lo.Unix(), hi.Unix())
	if err != nil {
		return nil, fmt.Errorf("day replies: %w", err)
	}
	defer rows.Close()
	var out []workday.Reply
	for rows.Next() {
		var (
			at                int64
			source, tool, key string
		)
		if err := rows.Scan(&at, &source, &tool, &key); err != nil {
			return nil, fmt.Errorf("scan day reply: %w", err)
		}
		out = append(out, workday.Reply{At: time.Unix(at, 0), Source: source, Tool: tool, SessionKey: key})
	}
	return out, rows.Err()
}

func (s *Store) PruneDay(cutoff time.Time) error {
	for _, table := range []string{"day_events", "day_replies"} {
		if _, err := s.db.Exec(`DELETE FROM `+table+` WHERE at < ?`, cutoff.Unix()); err != nil {
			return fmt.Errorf("prune %s: %w", table, err)
		}
	}
	return nil
}
