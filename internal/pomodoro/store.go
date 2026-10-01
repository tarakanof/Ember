package pomodoro

import (
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Store persists completed/ended phases and key/value settings in SQLite.
type Store struct {
	db       *sql.DB
	phaseGen atomic.Uint64
}

const schema = `
CREATE TABLE IF NOT EXISTS phases (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at  INTEGER NOT NULL,
  ended_at    INTEGER NOT NULL,
  phase       TEXT NOT NULL,
  planned_sec INTEGER NOT NULL,
  actual_sec  INTEGER NOT NULL,
  completed   INTEGER NOT NULL,
  reason      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_phases_ended ON phases(ended_at);
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS activity (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  recorded_at  INTEGER NOT NULL,
  source       TEXT NOT NULL,
  tool         TEXT NOT NULL,
  session_key  TEXT NOT NULL,
  state        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_activity_recorded ON activity(recorded_at);
`

// Open opens (creating if needed) the SQLite database at path and ensures the
// schema exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// RecordPhase inserts a row for an ended phase.
func (s *Store) RecordPhase(r PhaseResult, started, ended time.Time) error {
	completed := 0
	if r.Completed {
		completed = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO phases (started_at, ended_at, phase, planned_sec, actual_sec, completed, reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		started.Unix(), ended.Unix(), string(r.Phase), r.PlannedSec, r.ActualSec, completed, r.Reason,
	)
	if err != nil {
		return fmt.Errorf("record phase: %w", err)
	}
	s.phaseGen.Add(1)
	return nil
}

// PhaseGen returns a counter that advances on every successful RecordPhase.
func (s *Store) PhaseGen() uint64 { return s.phaseGen.Load() }

// DayStat is a per-day rollup of completed focus phases.
type DayStat struct {
	Date           string `json:"date"` // YYYY-MM-DD in the query's timezone
	CompletedFocus int    `json:"completed_focus"`
	FocusMin       int    `json:"focus_min"`
}

func dayBounds(t time.Time) (int64, int64) {
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return start.Unix(), start.AddDate(0, 0, 1).Unix()
}

func (s *Store) dayStat(t time.Time) (DayStat, error) {
	lo, hi := dayBounds(t)
	row := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(actual_sec), 0)
		   FROM phases
		  WHERE phase = ? AND completed = 1 AND ended_at >= ? AND ended_at < ?`,
		string(PhaseFocus), lo, hi,
	)
	var count, secs int
	if err := row.Scan(&count, &secs); err != nil {
		return DayStat{}, fmt.Errorf("day stat: %w", err)
	}
	return DayStat{
		Date:           t.Format("2006-01-02"),
		CompletedFocus: count,
		FocusMin:       secs / 60,
	}, nil
}

// Today returns the rollup for the day containing now.
func (s *Store) Today(now time.Time) (DayStat, error) {
	return s.dayStat(now)
}

// History returns per-day rollups for the most recent `days` days, most recent
// first (index 0 is the day containing now).
func (s *Store) History(now time.Time, days int) ([]DayStat, error) {
	if days < 1 {
		days = 1
	}
	out := make([]DayStat, 0, days)
	for i := 0; i < days; i++ {
		d, err := s.dayStat(now.AddDate(0, 0, -i))
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// Streak returns the number of consecutive days (ending today) that have at
// least one completed focus phase.
func (s *Store) Streak(now time.Time) (int, error) {
	streak := 0
	for i := 0; ; i++ {
		d, err := s.dayStat(now.AddDate(0, 0, -i))
		if err != nil {
			return 0, err
		}
		if d.CompletedFocus == 0 {
			break
		}
		streak++
	}
	return streak, nil
}

// GetSetting returns the stored value for key.
func (s *Store) GetSetting(key string) (value string, ok bool, err error) {
	row := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key)
	switch err := row.Scan(&value); err {
	case nil:
		return value, true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("get setting %q: %w", key, err)
	}
}

// PutSetting upserts a setting.
func (s *Store) PutSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("put setting %q: %w", key, err)
	}
	return nil
}
