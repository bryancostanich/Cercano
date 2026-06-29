package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	goopenai "github.com/sashabaranov/go-openai"
)

// normalizingDoer wraps an HTTPDoer to repair per-backend response quirks before
// go-openai parses them: it retries transient statuses (replaying the request
// body) and rewrites array-shaped error bodies into the object shape go-openai's
// ErrorResponse expects. 2xx (including streaming) responses pass through
// untouched — their bodies are never buffered here.
type normalizingDoer struct {
	next   goopenai.HTTPDoer
	quirks Quirks
}

func (d *normalizingDoer) Do(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if attempt > 1 && req.GetBody != nil {
			b, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = b
		}
		resp, err := d.next.Do(req)
		if d.shouldRetry(resp, err, attempt) {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			if !sleepBackoff(req.Context(), d.quirks.Retry.BaseDelay, attempt) {
				return nil, req.Context().Err()
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		return d.normalize(resp), nil
	}
}

// shouldRetry reports whether to retry. Transport errors (context, DNS, reset)
// are not retried here — only the configured transient HTTP statuses, and only
// while attempts remain.
func (d *normalizingDoer) shouldRetry(resp *http.Response, err error, attempt int) bool {
	rp := d.quirks.Retry
	if attempt >= rp.MaxAttempts || err != nil || resp == nil {
		return false
	}
	for _, s := range rp.OnStatus {
		if resp.StatusCode == s {
			return true
		}
	}
	return false
}

// sleepBackoff waits BaseDelay*2^(attempt-1), aborting on ctx cancellation.
// Returns false if the context ended during the wait.
func sleepBackoff(ctx context.Context, base time.Duration, attempt int) bool {
	t := time.NewTimer(base << (attempt - 1))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// normalize rewrites an array-shaped error body to the object shape when the
// backend needs it. Only non-2xx bodies are read; 2xx responses pass through so
// streaming bodies are never consumed.
func (d *normalizingDoer) normalize(resp *http.Response) *http.Response {
	if !d.quirks.NormalizeErrors || resp.StatusCode < 400 {
		return resp
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		resp.ContentLength = 0
		return resp
	}
	if fixed, ok := arrayErrorToObject(body); ok {
		body = fixed
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp
}

// arrayErrorToObject unwraps a `[{...}]` error body to its first object element.
// Returns (newBody, true) only when the body is a non-empty JSON array.
func arrayErrorToObject(body []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(trimmed, &arr); err != nil || len(arr) == 0 {
		return nil, false
	}
	return arr[0], true
}
