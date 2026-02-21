package metrics

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecordAndTodayUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.Record(ctx, CallRecord{
		ID:           "call-1",
		SessionID:    "sess-1",
		Timestamp:    time.Now(),
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-20250514",
		InputTokens:  1000,
		OutputTokens: 500,
		LatencyMs:    250,
		Status:       "ok",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.Record(ctx, CallRecord{
		ID:           "call-2",
		SessionID:    "sess-1",
		Timestamp:    time.Now(),
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-20250514",
		InputTokens:  2000,
		OutputTokens: 1000,
		LatencyMs:    300,
		Status:       "ok",
	})
	if err != nil {
		t.Fatal(err)
	}

	u, err := s.TodayUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 3000 {
		t.Errorf("InputTokens = %d, want 3000", u.InputTokens)
	}
	if u.OutputTokens != 1500 {
		t.Errorf("OutputTokens = %d, want 1500", u.OutputTokens)
	}
	if u.CallCount != 2 {
		t.Errorf("CallCount = %d, want 2", u.CallCount)
	}
}

func TestTodayUsageEmpty(t *testing.T) {
	s := newTestStore(t)
	u, err := s.TodayUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.CallCount != 0 {
		t.Errorf("CallCount = %d, want 0 for empty db", u.CallCount)
	}
}

func TestSummary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	yesterday := now.Add(-24 * time.Hour)

	s.Record(ctx, CallRecord{
		ID: "c1", SessionID: "s1", Timestamp: yesterday,
		Provider: "anthropic", Model: "m", InputTokens: 100, OutputTokens: 50,
		LatencyMs: 10, Status: "ok",
	})
	s.Record(ctx, CallRecord{
		ID: "c2", SessionID: "s1", Timestamp: now,
		Provider: "anthropic", Model: "m", InputTokens: 200, OutputTokens: 100,
		LatencyMs: 20, Status: "ok",
	})

	days, err := s.Summary(ctx, yesterday.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(days) < 1 {
		t.Fatal("expected at least 1 day in summary")
	}
}

func TestSchemaIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	// Opening again should not fail (CREATE IF NOT EXISTS).
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal("second open failed:", err)
	}
	s2.Close()
}

func TestWALMode(t *testing.T) {
	s := newTestStore(t)
	var mode string
	err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestDefaultToolCalls(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Record with empty ToolCalls — should default to "[]".
	err := s.Record(ctx, CallRecord{
		ID: "c1", SessionID: "s1", Timestamp: time.Now(),
		Provider: "anthropic", Model: "m", InputTokens: 100, OutputTokens: 50,
		LatencyMs: 10, Status: "ok",
	})
	if err != nil {
		t.Fatal(err)
	}

	var tc string
	err = s.db.QueryRow("SELECT tool_calls FROM calls WHERE id = 'c1'").Scan(&tc)
	if err != nil {
		t.Fatal(err)
	}
	if tc != "[]" {
		t.Errorf("tool_calls = %q, want %q", tc, "[]")
	}
}
