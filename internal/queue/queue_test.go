package queue

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TolgaOk/agentgate/internal/provider"
)

// mockProvider is a test double for provider.Provider.
type mockProvider struct {
	chatFn   func(ctx context.Context, req provider.Request) (provider.Response, error)
	streamFn func(ctx context.Context, req provider.Request) (<-chan provider.StreamChunk, error)
}

func (m *mockProvider) Chat(ctx context.Context, req provider.Request) (provider.Response, error) {
	return m.chatFn(ctx, req)
}

func (m *mockProvider) ChatStream(ctx context.Context, req provider.Request) (<-chan provider.StreamChunk, error) {
	return m.streamFn(ctx, req)
}

func TestChatPassthrough(t *testing.T) {
	mock := &mockProvider{
		chatFn: func(_ context.Context, _ provider.Request) (provider.Response, error) {
			return provider.Response{Text: "ok"}, nil
		},
	}
	q := New(mock, Config{GlobalLimit: 100, ProviderLimit: 100}, nil)
	resp, err := q.Chat(context.Background(), provider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" {
		t.Errorf("Text = %q, want ok", resp.Text)
	}
}

func TestRetryOnTransientError(t *testing.T) {
	var calls atomic.Int32
	mock := &mockProvider{
		chatFn: func(_ context.Context, _ provider.Request) (provider.Response, error) {
			n := calls.Add(1)
			if n < 3 {
				return provider.Response{}, fmt.Errorf("HTTP 429 rate limited")
			}
			return provider.Response{Text: "ok"}, nil
		},
	}
	q := New(mock, Config{GlobalLimit: 100, ProviderLimit: 100}, nil)
	resp, err := q.Chat(context.Background(), provider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" {
		t.Errorf("Text = %q, want ok", resp.Text)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestNoRetryOnNonTransient(t *testing.T) {
	var calls atomic.Int32
	mock := &mockProvider{
		chatFn: func(_ context.Context, _ provider.Request) (provider.Response, error) {
			calls.Add(1)
			return provider.Response{}, fmt.Errorf("HTTP 401 unauthorized")
		},
	}
	q := New(mock, Config{GlobalLimit: 100, ProviderLimit: 100}, nil)
	_, err := q.Chat(context.Background(), provider.Request{})
	if err == nil {
		t.Error("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry for 401)", calls.Load())
	}
}

func TestPassthroughWithNoMetrics(t *testing.T) {
	mock := &mockProvider{
		chatFn: func(_ context.Context, _ provider.Request) (provider.Response, error) {
			return provider.Response{Text: "ok"}, nil
		},
	}
	// nil metrics store = no concurrency limiting.
	q := New(mock, Config{GlobalLimit: 100, ProviderLimit: 100}, nil)

	for range 5 {
		resp, err := q.Chat(context.Background(), provider.Request{})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Text != "ok" {
			t.Errorf("Text = %q, want ok", resp.Text)
		}
	}
}

func TestContextCancellation(t *testing.T) {
	mock := &mockProvider{
		chatFn: func(_ context.Context, _ provider.Request) (provider.Response, error) {
			return provider.Response{}, fmt.Errorf("HTTP 429 rate limited")
		},
	}
	q := New(mock, Config{GlobalLimit: 100, ProviderLimit: 100}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := q.Chat(ctx, provider.Request{})
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestCircuitBreakerTrips(t *testing.T) {
	var calls atomic.Int32
	mock := &mockProvider{
		chatFn: func(_ context.Context, _ provider.Request) (provider.Response, error) {
			calls.Add(1)
			return provider.Response{}, fmt.Errorf("HTTP 401 server error")
		},
	}
	q := New(mock, Config{GlobalLimit: 100, ProviderLimit: 100}, nil)

	// Make enough failing calls to trip the breaker (5 consecutive).
	for range 6 {
		q.Chat(context.Background(), provider.Request{})
	}

	callsBefore := calls.Load()
	// Next call should fail via breaker without reaching the provider.
	_, err := q.Chat(context.Background(), provider.Request{})
	if err == nil {
		t.Error("expected error from tripped breaker")
	}
	if calls.Load() != callsBefore {
		t.Error("breaker should have prevented the call from reaching provider")
	}
}
