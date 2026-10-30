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

const (
	DayExactWindow   = 24 * time.Hour
	DayExactSameSpan = 2 * time.Second
)

const dayReplyCovered = `NOT EXISTS (SELECT 1 FROM day_replies e
  WHERE e.exact = 1 AND e.source = r.source AND e.tool = r.tool AND e.at >= r.at - ?1 AND e.at <= r.at + ?1)`

func migrateDayReplies(db *sql.DB) error {
	cols := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(day_replies)`)
	if err != nil {
		return fmt.Errorf("migrate day replies: %w", err)
	}
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typ        string
			def              sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("migrate day replies: %w", err)
		}
		cols[name] = true
	}
	rows.Close()
	steps := []string{}
	if !cols["repo"] {
		steps = append(steps, `ALTER TABLE day_replies ADD COLUMN repo TEXT NOT NULL DEFAULT ''`)
	}
	if !cols["exact"] {
		steps = append(steps, `ALTER TABLE day_replies ADD COLUMN exact INTEGER NOT NULL DEFAULT 0`)
	}
	steps = append(steps,
		`DELETE FROM day_replies WHERE id NOT IN (SELECT MIN(id) FROM day_replies GROUP BY source, tool, session_key, at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_day_replies_key ON day_replies(source, tool, session_key, at)`,
		`CREATE INDEX IF NOT EXISTS idx_day_replies_exact ON day_replies(source, tool, at) WHERE exact = 1`,
	)
	for _, q := range steps {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migrate day replies: %w", err)
		}
	}
	return nil
}

func (s *Store) RecordDayReply(r workday.Reply) error {
	_, err := s.RecordDayReplies([]workday.Reply{r})
	return err
}

func (s *Store) RecordDayReplies(rs []workday.Reply) (inserted []bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("record day replies: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	inserted = make([]bool, len(rs))
	span := int64(DayExactSameSpan / time.Second)
	for i, r := range rs {
		at := r.At.Unix()
		if r.Exact {
			var dup int
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM day_replies WHERE source = ? AND tool = ? AND session_key = ?
			    AND at BETWEEN ? AND ? AND exact = 1)`, r.Source, r.Tool, r.SessionKey, at-span, at+span).Scan(&dup); err != nil {
				return nil, fmt.Errorf("record day reply: %w", err)
			}
			if dup == 1 {
				continue
			}
		}
		exact := 0
		if r.Exact {
			exact = 1
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO day_replies (at, source, tool, session_key, repo, exact) VALUES (?, ?, ?, ?, ?, ?)`,
			at, r.Source, r.Tool, r.SessionKey, r.Repo, exact)
		if err != nil {
			return nil, fmt.Errorf("record day reply: %w", err)
		}
		n, _ := res.RowsAffected()
		inserted[i] = n == 1
		if !inserted[i] && r.Exact {
			if _, err := tx.Exec(`UPDATE day_replies SET exact = 1, repo = ? WHERE source = ? AND tool = ? AND session_key = ? AND at = ? AND exact = 0`,
				r.Repo, r.Source, r.Tool, r.SessionKey, at); err != nil {
				return nil, fmt.Errorf("record day reply: %w", err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("record day replies: %w", err)
	}
	return inserted, nil
}

func (s *Store) ExactDayReplyPairs(since time.Time) (map[[2]string]time.Time, error) {
	rows, err := s.db.Query(`SELECT source, tool, MAX(at) FROM day_replies WHERE exact = 1 AND at >= ? GROUP BY source, tool`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("exact day replies: %w", err)
	}
	defer rows.Close()
	out := map[[2]string]time.Time{}
	for rows.Next() {
		var (
			source, tool string
			at           int64
		)
		if err := rows.Scan(&source, &tool, &at); err != nil {
			return nil, fmt.Errorf("scan exact day reply: %w", err)
		}
		out[[2]string{source, tool}] = time.Unix(at, 0)
	}
	return out, rows.Err()
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
	q := `SELECT COUNT(*) FROM day_replies r WHERE r.at >= ?2 AND (r.exact = 1 OR ` + dayReplyCovered + `)`
	if err := s.db.QueryRow(q, int64(DayExactWindow/time.Second), since.Unix()).Scan(&n); err != nil {
		return 0, fmt.Errorf("count day replies: %w", err)
	}
	return n, nil
}

func (s *Store) DayRepliesBetween(lo, hi time.Time) ([]workday.Reply, error) {
	rows, err := s.db.Query(
		`SELECT r.at, r.source, r.tool, r.session_key, r.repo, r.exact FROM day_replies r
		  WHERE r.at >= ?2 AND r.at < ?3 AND (r.exact = 1 OR `+dayReplyCovered+`)
		  ORDER BY r.at, r.id`,
		int64(DayExactWindow/time.Second), lo.Unix(), hi.Unix())
	if err != nil {
		return nil, fmt.Errorf("day replies: %w", err)
	}
	defer rows.Close()
	var out []workday.Reply
	for rows.Next() {
		var (
			at, exact               int64
			source, tool, key, repo string
		)
		if err := rows.Scan(&at, &source, &tool, &key, &repo, &exact); err != nil {
			return nil, fmt.Errorf("scan day reply: %w", err)
		}
		out = append(out, workday.Reply{At: time.Unix(at, 0), Source: source, Tool: tool, SessionKey: key, Repo: repo, Exact: exact == 1})
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
