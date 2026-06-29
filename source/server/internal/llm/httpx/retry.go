// Package httpx holds small, provider-neutral HTTP helpers shared by the cloud
// LLM clients.
package httpx

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Doer is the minimal HTTP "do" interface, satisfied by *http.Client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// RetryPolicy controls transient-failure retries.
type RetryPolicy struct {
	MaxAttempts int           // total attempts incl. the first; <2 disables retry
	BaseDelay   time.Duration // first backoff; doubles each subsequent attempt
	OnStatus    []int         // HTTP statuses that trigger a retry
}

// RetryTransport wraps a Doer, retrying transient HTTP statuses with exponential
// backoff and replaying the request body each attempt. Transport-level errors
// (context, DNS, connection reset) are NOT retried — only the configured
// statuses. The retried response body is drained and closed before the next try.
type RetryTransport struct {
	Next   Doer
	Policy RetryPolicy
}

func (t *RetryTransport) Do(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if attempt > 1 && req.GetBody != nil {
			b, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = b
		}
		resp, err := t.Next.Do(req)
		if t.shouldRetry(resp, err, attempt) {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			if !sleepBackoff(req.Context(), t.Policy.BaseDelay, attempt) {
				return nil, req.Context().Err()
			}
			continue
		}
		return resp, err
	}
}

func (t *RetryTransport) shouldRetry(resp *http.Response, err error, attempt int) bool {
	if attempt >= t.Policy.MaxAttempts || err != nil || resp == nil {
		return false
	}
	for _, s := range t.Policy.OnStatus {
		if resp.StatusCode == s {
			return true
		}
	}
	return false
}

// sleepBackoff waits BaseDelay*2^(attempt-1), aborting on ctx cancellation.
// Returns false if the context ended during the wait.
func sleepBackoff(ctx context.Context, base time.Duration, attempt int) bool {
	tm := time.NewTimer(base << (attempt - 1))
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-tm.C:
		return true
	}
}
