package queue

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/TolgaOk/agentgate/internal/metrics"
	"github.com/TolgaOk/agentgate/internal/provider"
)

const (
	maxRetries        = 3
	pollInterval      = 500 * time.Millisecond
	heartbeatInterval = 500 * time.Millisecond
)

type Queue struct {
	inner         provider.Provider
	breaker       *gobreaker.CircuitBreaker[any]
	store         *metrics.Store
	providerName  string
	model         string
	sessionID     string
	globalLimit   int
	providerLimit int
}

type Config struct {
	GlobalLimit   int
	ProviderLimit int
	ProviderName  string
	Model         string
	SessionID     string
}

func New(p provider.Provider, cfg Config, store *metrics.Store) *Queue {
	breaker := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name: "llm",
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		Timeout: 30 * time.Second,
	})

	return &Queue{
		inner:         p,
		breaker:       breaker,
		store:         store,
		providerName:  cfg.ProviderName,
		model:         cfg.Model,
		sessionID:     cfg.SessionID,
		globalLimit:   cfg.GlobalLimit,
		providerLimit: cfg.ProviderLimit,
	}
}

func (q *Queue) Chat(ctx context.Context, req provider.Request) (provider.Response, error) {
	callID, err := q.acquire(ctx)
	if err != nil {
		return provider.Response{}, err
	}
	stopHB := q.startHeartbeat(ctx, callID)
	defer stopHB()

	resp, err := q.callWithRetry(ctx, func() (any, error) {
		return q.inner.Chat(ctx, req)
	})
	if err != nil {
		q.fail(ctx, callID, err.Error())
		return provider.Response{}, err
	}

	r := resp.(provider.Response)
	q.complete(ctx, callID, r.Usage.InputTokens, r.Usage.OutputTokens, 0)
	return r, nil
}

func (q *Queue) ChatStream(ctx context.Context, req provider.Request) (<-chan provider.StreamChunk, error) {
	callID, err := q.acquire(ctx)
	if err != nil {
		return nil, err
	}
	stopHB := q.startHeartbeat(ctx, callID)

	ch, err := q.callWithRetry(ctx, func() (any, error) {
		return q.inner.ChatStream(ctx, req)
	})
	if err != nil {
		stopHB()
		q.fail(ctx, callID, err.Error())
		return nil, err
	}

	innerCh := ch.(<-chan provider.StreamChunk)
	return q.wrapStream(ctx, callID, innerCh, stopHB), nil
}

// acquire enqueues a call and waits until a slot is available.
func (q *Queue) acquire(ctx context.Context) (string, error) {
	callID := fmt.Sprintf("call-%d-%d", os.Getpid(), time.Now().UnixNano())

	if q.store == nil {
		return callID, nil
	}

	if err := q.store.Enqueue(ctx, callID, q.sessionID, q.providerName, q.model); err != nil {
		return "", err
	}

	notified := false
	for {
		ok, err := q.store.TryAcquire(ctx, callID, q.providerName, q.globalLimit, q.providerLimit)
		if err != nil {
			q.store.Fail(ctx, callID, err.Error())
			return "", err
		}
		if ok {
			return callID, nil
		}

		if !notified {
			fmt.Fprintf(os.Stderr, "waiting for rate limiter...\n")
			notified = true
		}

		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			q.store.Fail(ctx, callID, "cancelled")
			return "", ctx.Err()
		}
	}
}

// startHeartbeat spawns a goroutine that periodically updates the heartbeat
// timestamp in the store. Returns a function to stop the goroutine.
func (q *Queue) startHeartbeat(ctx context.Context, callID string) func() {
	if q.store == nil {
		return func() {}
	}
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				q.store.Heartbeat(ctx, callID)
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() { close(stopCh) }
}

// wrapStream proxies the inner channel and calls complete/fail when done.
func (q *Queue) wrapStream(ctx context.Context, callID string, inner <-chan provider.StreamChunk, stopHB func()) <-chan provider.StreamChunk {
	out := make(chan provider.StreamChunk)
	go func() {
		defer close(out)
		defer stopHB()
		var totalIn, totalOut int
		start := time.Now()

		for chunk := range inner {
			if chunk.Kind == provider.ChunkUsage && chunk.Usage != nil {
				totalIn += chunk.Usage.InputTokens
				totalOut += chunk.Usage.OutputTokens
			}
			if chunk.Kind == provider.ChunkError && chunk.Err != nil {
				q.fail(ctx, callID, chunk.Err.Error())
			}

			select {
			case out <- chunk:
			case <-ctx.Done():
				q.fail(ctx, callID, "cancelled")
				return
			}
		}

		q.complete(ctx, callID, totalIn, totalOut, time.Since(start).Milliseconds())
	}()
	return out
}

func (q *Queue) complete(_ context.Context, callID string, inTok, outTok int, latencyMs int64) {
	if q.store == nil {
		return
	}
	q.store.Complete(context.Background(), callID, metrics.CallRecord{
		InputTokens:  inTok,
		OutputTokens: outTok,
		LatencyMs:    latencyMs,
	})
}

func (q *Queue) fail(_ context.Context, callID, reason string) {
	if q.store == nil {
		return
	}
	q.store.Fail(context.Background(), callID, reason)
}

func (q *Queue) callWithRetry(ctx context.Context, fn func() (any, error)) (any, error) {
	var lastErr error
	for attempt := range maxRetries {
		result, err := q.breaker.Execute(fn)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
		if attempt < maxRetries-1 {
			if !backoff(ctx, attempt) {
				return nil, ctx.Err()
			}
		}
	}
	return nil, fmt.Errorf("queue: all %d retries failed: %w", maxRetries, lastErr)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "500") ||
		strings.Contains(msg, "529") ||
		strings.Contains(msg, "overloaded")
}

func backoff(ctx context.Context, attempt int) bool {
	base := time.Duration(math.Pow(2, float64(attempt))) * time.Second
	jitter := time.Duration(rand.Int64N(int64(base / 2)))
	delay := base + jitter

	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}
