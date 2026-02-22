package metrics

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusError   = "error"

	staleTimeout = 5 * time.Minute
)

type Store struct {
	db *sql.DB
}

type CallRecord struct {
	ID           string
	SessionID    string
	Provider     string
	Model        string
	InputTokens  int
	OutputTokens int
	LatencyMs    int64
	ToolCalls    string
}

type Usage struct {
	InputTokens  int
	OutputTokens int
	CallCount    int
}

type DaySummary struct {
	Date         string
	InputTokens  int
	OutputTokens int
	CallCount    int
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("metrics: open db: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("metrics: set WAL: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("metrics: create schema: %w", err)
	}
	s := &Store{db: db}
	s.CleanupStale(context.Background())
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Enqueue inserts a pending call row.
func (s *Store) Enqueue(ctx context.Context, id, sessionID, prov, model string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO calls (id, session_id, pid, created_at, provider, model, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, sessionID, os.Getpid(), now(), prov, model, StatusPending,
	)
	if err != nil {
		return fmt.Errorf("metrics: enqueue: %w", err)
	}
	return nil
}

// TryAcquire atomically transitions a pending call to running,
// but only if both the global and per-provider concurrency limits allow it.
// Returns true if the call was acquired, false if limits are reached.
func (s *Store) TryAcquire(ctx context.Context, id, prov string, globalLimit, providerLimit int) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE calls SET status = ?, started_at = ?
		 WHERE id = ? AND status = ?
		 AND (SELECT COUNT(*) FROM calls WHERE status = ?) < ?
		 AND (SELECT COUNT(*) FROM calls WHERE status = ? AND provider = ?) < ?`,
		StatusRunning, now(),
		id, StatusPending,
		StatusRunning, globalLimit,
		StatusRunning, prov, providerLimit,
	)
	if err != nil {
		return false, fmt.Errorf("metrics: try acquire: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("metrics: try acquire: %w", err)
	}
	return n > 0, nil
}

// Complete marks a call as successfully finished with metrics.
func (s *Store) Complete(ctx context.Context, id string, r CallRecord) error {
	if r.ToolCalls == "" {
		r.ToolCalls = "[]"
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE calls SET status = ?, finished_at = ?,
		 input_tokens = ?, output_tokens = ?, latency_ms = ?, tool_calls = ?
		 WHERE id = ?`,
		StatusOK, now(),
		r.InputTokens, r.OutputTokens, r.LatencyMs, r.ToolCalls,
		id,
	)
	if err != nil {
		return fmt.Errorf("metrics: complete: %w", err)
	}
	return nil
}

// Fail marks a pending or running call as error.
func (s *Store) Fail(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE calls SET status = ?, finished_at = ?, tool_calls = ?
		 WHERE id = ? AND status IN (?, ?)`,
		StatusError, now(), reason,
		id, StatusPending, StatusRunning,
	)
	if err != nil {
		return fmt.Errorf("metrics: fail: %w", err)
	}
	return nil
}

// CleanupStale marks old pending/running rows as error (crashed processes).
func (s *Store) CleanupStale(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-staleTimeout).Format(time.RFC3339)
	s.db.ExecContext(ctx,
		`UPDATE calls SET status = ?, finished_at = ?
		 WHERE status IN (?, ?) AND created_at < ?`,
		StatusError, now(),
		StatusPending, StatusRunning, cutoff,
	)
}

// Record inserts a completed call in one shot (backwards compat).
func (s *Store) Record(ctx context.Context, r CallRecord) error {
	if r.ToolCalls == "" {
		r.ToolCalls = "[]"
	}
	ts := now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO calls (id, session_id, pid, created_at, started_at, finished_at,
		 provider, model, input_tokens, output_tokens, latency_ms, status, tool_calls)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.SessionID, os.Getpid(), ts, ts, ts,
		r.Provider, r.Model, r.InputTokens, r.OutputTokens,
		r.LatencyMs, StatusOK, r.ToolCalls,
	)
	if err != nil {
		return fmt.Errorf("metrics: record: %w", err)
	}
	return nil
}

// Usage returns aggregate token usage since the given time.
func (s *Store) Usage(ctx context.Context, since time.Time) (Usage, error) {
	var u Usage
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens), 0),
		        COALESCE(SUM(output_tokens), 0),
		        COUNT(*)
		 FROM calls
		 WHERE created_at >= ? AND status = ?`,
		since.UTC().Format(time.RFC3339), StatusOK,
	).Scan(&u.InputTokens, &u.OutputTokens, &u.CallCount)
	if err != nil {
		return Usage{}, fmt.Errorf("metrics: usage: %w", err)
	}
	return u, nil
}

// Summary returns per-day aggregation since the given time.
func (s *Store) Summary(ctx context.Context, since time.Time) ([]DaySummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DATE(created_at) AS day,
		        SUM(input_tokens), SUM(output_tokens), COUNT(*)
		 FROM calls
		 WHERE created_at >= ? AND status = ?
		 GROUP BY day ORDER BY day`,
		since.UTC().Format(time.RFC3339), StatusOK,
	)
	if err != nil {
		return nil, fmt.Errorf("metrics: summary: %w", err)
	}
	defer rows.Close()

	var out []DaySummary
	for rows.Next() {
		var d DaySummary
		if err := rows.Scan(&d.Date, &d.InputTokens, &d.OutputTokens, &d.CallCount); err != nil {
			return nil, fmt.Errorf("metrics: summary scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
