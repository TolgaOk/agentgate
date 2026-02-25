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

func TestRecordAndUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.Record(ctx, CallRecord{
		ID: "c1", SessionID: "s1",
		Provider: "anthropic", Model: "m",
		InputTokens: 1000, OutputTokens: 500, LatencyMs: 250,
	})
	s.Record(ctx, CallRecord{
		ID: "c2", SessionID: "s1",
		Provider: "anthropic", Model: "m",
		InputTokens: 2000, OutputTokens: 1000, LatencyMs: 300,
	})

	since := time.Now().UTC().Truncate(24 * time.Hour)
	u, err := s.Usage(ctx, since)
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

func TestUsageEmpty(t *testing.T) {
	s := newTestStore(t)
	since := time.Now().UTC().Truncate(24 * time.Hour)
	u, err := s.Usage(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	if u.CallCount != 0 {
		t.Errorf("CallCount = %d, want 0", u.CallCount)
	}
}

func TestTryAcquireLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.Enqueue(ctx, "q1", "s1", "anthropic", "m")

	// Should acquire — nothing running.
	ok, err := s.TryAcquire(ctx, "q1", "anthropic", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("TryAcquire should succeed with nothing running")
	}

	// Enqueue another for same provider — should fail (per-provider limit 1).
	s.Enqueue(ctx, "q2", "s1", "anthropic", "m")
	ok, _ = s.TryAcquire(ctx, "q2", "anthropic", 2, 1)
	if ok {
		t.Fatal("TryAcquire should fail — per-provider limit reached")
	}

	// Complete q1 — now q2 should acquire.
	s.Complete(ctx, "q1", CallRecord{InputTokens: 100, OutputTokens: 50, LatencyMs: 10})
	ok, _ = s.TryAcquire(ctx, "q2", "anthropic", 2, 1)
	if !ok {
		t.Fatal("TryAcquire should succeed after q1 completed")
	}
}

func TestTryAcquireGlobalLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Two different providers, global limit 1.
	s.Enqueue(ctx, "a1", "s1", "anthropic", "m")
	s.Enqueue(ctx, "b1", "s1", "openrouter", "m")

	ok, _ := s.TryAcquire(ctx, "a1", "anthropic", 1, 5)
	if !ok {
		t.Fatal("first acquire should succeed")
	}

	ok, _ = s.TryAcquire(ctx, "b1", "openrouter", 1, 5)
	if ok {
		t.Fatal("second acquire should fail — global limit 1")
	}
}

func TestFail(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.Enqueue(ctx, "f1", "s1", "anthropic", "m")
	s.TryAcquire(ctx, "f1", "anthropic", 5, 5)
	s.Fail(ctx, "f1", "connection timeout")

	// Should be able to acquire again (f1 no longer running).
	s.Enqueue(ctx, "f2", "s1", "anthropic", "m")
	ok, _ := s.TryAcquire(ctx, "f2", "anthropic", 5, 1)
	if !ok {
		t.Fatal("TryAcquire should succeed after f1 failed")
	}
}

func TestTryAcquireIgnoresStaleHeartbeat(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Directly insert a "running" row with a stale heartbeat (10 seconds ago).
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO calls (id, session_id, pid, created_at, started_at, provider, model, status, last_heartbeat)
		 VALUES ('stale1', 's1', 999, datetime('now'), datetime('now'), 'anthropic', 'm', 'running',
		         datetime('now', '-10 seconds'))`,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Enqueue a new call — should acquire because stale1's heartbeat is old.
	s.Enqueue(ctx, "new1", "s1", "anthropic", "m")
	ok, err := s.TryAcquire(ctx, "new1", "anthropic", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("TryAcquire should succeed — stale heartbeat should be ignored")
	}
}

func TestHeartbeatUpdatesTimestamp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.Enqueue(ctx, "hb1", "s1", "anthropic", "m")
	s.TryAcquire(ctx, "hb1", "anthropic", 5, 5)

	// Record initial heartbeat value.
	var before string
	s.db.QueryRow("SELECT last_heartbeat FROM calls WHERE id = 'hb1'").Scan(&before)

	// Update heartbeat.
	if err := s.Heartbeat(ctx, "hb1"); err != nil {
		t.Fatal(err)
	}

	var after string
	s.db.QueryRow("SELECT last_heartbeat FROM calls WHERE id = 'hb1'").Scan(&after)

	if after == "" {
		t.Fatal("last_heartbeat should not be empty after Heartbeat()")
	}
	// The heartbeat should be at least as recent as before.
	if after < before {
		t.Errorf("heartbeat went backwards: before=%s, after=%s", before, after)
	}
}

func TestSchemaIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

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

	s.Record(ctx, CallRecord{
		ID: "c1", SessionID: "s1",
		Provider: "anthropic", Model: "m",
		InputTokens: 100, OutputTokens: 50, LatencyMs: 10,
	})

	var tc string
	err := s.db.QueryRow("SELECT tool_calls FROM calls WHERE id = 'c1'").Scan(&tc)
	if err != nil {
		t.Fatal(err)
	}
	if tc != "[]" {
		t.Errorf("tool_calls = %q, want %q", tc, "[]")
	}
}
