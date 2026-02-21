package metrics

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed queries/insert_call.sql
var queryInsertCall string

//go:embed queries/today_usage.sql
var queryTodayUsage string

//go:embed queries/summary.sql
var querySummary string

type Store struct {
	db *sql.DB
}

type CallRecord struct {
	ID           string
	SessionID    string
	Timestamp    time.Time
	Provider     string
	Model        string
	InputTokens  int
	OutputTokens int
	LatencyMs    int64
	Status       string
	ToolCalls    string // JSON array
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
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Record(ctx context.Context, r CallRecord) error {
	if r.ToolCalls == "" {
		r.ToolCalls = "[]"
	}
	_, err := s.db.ExecContext(ctx, queryInsertCall,
		r.ID, r.SessionID, r.Timestamp.UTC().Format(time.RFC3339),
		r.Provider, r.Model, r.InputTokens, r.OutputTokens,
		r.LatencyMs, r.Status, r.ToolCalls,
	)
	if err != nil {
		return fmt.Errorf("metrics: record: %w", err)
	}
	return nil
}

func (s *Store) TodayUsage(ctx context.Context) (Usage, error) {
	today := time.Now().UTC().Format("2006-01-02")
	row := s.db.QueryRowContext(ctx, queryTodayUsage, today)

	var u Usage
	if err := row.Scan(&u.InputTokens, &u.OutputTokens, &u.CallCount); err != nil {
		return Usage{}, fmt.Errorf("metrics: today usage: %w", err)
	}
	return u, nil
}

func (s *Store) Summary(ctx context.Context, since time.Time) ([]DaySummary, error) {
	rows, err := s.db.QueryContext(ctx, querySummary, since.UTC().Format(time.RFC3339))
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
