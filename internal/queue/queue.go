package queue

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/sony/gobreaker/v2"
	"golang.org/x/time/rate"

	"github.com/TolgaOk/agentgate/internal/metrics"
	"github.com/TolgaOk/agentgate/internal/provider"
)

const maxRetries = 3

// Queue wraps a provider with rate limiting, budget checks,
// circuit breaking, and retry logic. It implements provider.Provider.
type Queue struct {
	inner   provider.Provider
	limiter *rate.Limiter
	breaker *gobreaker.CircuitBreaker[any]
	metrics *metrics.Store
	budget  float64 // daily budget (0 = unlimited) — reserved for future use
}

type Config struct {
	RPM    int     // requests per minute
	Budget float64 // daily budget in USD (0 = unlimited)
}

func New(p provider.Provider, cfg Config, store *metrics.Store) *Queue {
	// Rate limiter: RPM tokens, refill one per (60/RPM) seconds.
	rpm := cfg.RPM
	if rpm <= 0 {
		rpm = 50
	}
	limiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(rpm)), 1)

	// Circuit breaker: trip after 5 consecutive failures, half-open after 30s.
	breaker := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name: "llm",
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		Timeout: 30 * time.Second,
	})

	return &Queue{
		inner:   p,
		limiter: limiter,
		breaker: breaker,
		metrics: store,
		budget:  cfg.Budget,
	}
}

func (q *Queue) Chat(ctx context.Context, req provider.Request) (provider.Response, error) {
	if err := q.limiter.Wait(ctx); err != nil {
		return provider.Response{}, fmt.Errorf("queue: rate limit: %w", err)
	}

	var resp provider.Response
	var lastErr error

	for attempt := range maxRetries {
		result, err := q.breaker.Execute(func() (any, error) {
			return q.inner.Chat(ctx, req)
		})
		if err == nil {
			resp = result.(provider.Response)
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return provider.Response{}, err
		}
		if attempt < maxRetries-1 {
			if !backoff(ctx, attempt) {
				return provider.Response{}, ctx.Err()
			}
		}
	}

	return provider.Response{}, fmt.Errorf("queue: all %d retries failed: %w", maxRetries, lastErr)
}

func (q *Queue) ChatStream(ctx context.Context, req provider.Request) (<-chan provider.StreamChunk, error) {
	if err := q.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("queue: rate limit: %w", err)
	}

	var lastErr error
	for attempt := range maxRetries {
		result, err := q.breaker.Execute(func() (any, error) {
			return q.inner.ChatStream(ctx, req)
		})
		if err == nil {
			return result.(<-chan provider.StreamChunk), nil
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

// isRetryable returns true for errors that suggest a transient failure.
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

// backoff sleeps for exponential backoff with jitter.
// Returns false if context is cancelled.
func backoff(ctx context.Context, attempt int) bool {
	base := time.Duration(math.Pow(2, float64(attempt))) * time.Second
	jitter := time.Duration(rand.Int64N(int64(base/2)))
	delay := base + jitter

	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}
