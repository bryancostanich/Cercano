package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func postReq(t *testing.T, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestRetryThenSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	resp, err := tr.Do(postReq(t, srv.URL, `{"x":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 || atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("status=%d hits=%d", resp.StatusCode, hits)
	}
}

func TestRetryExhausted(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	resp, err := tr.Do(postReq(t, srv.URL, `{"x":1}`))
	if err != nil {
		t.Fatalf("expected the 503 response, not an error: %v", err)
	}
	if resp.StatusCode != 503 || atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("status=%d hits=%d", resp.StatusCode, hits)
	}
}

func TestRetryBodyResent(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	if _, err := tr.Do(postReq(t, srv.URL, `{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("body not resent identically: %#v", bodies)
	}
}

func TestRetryContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, OnStatus: []int{503}}}

	done := make(chan struct{})
	go func() {
		req := postReq(t, srv.URL, `{"x":1}`).WithContext(ctx)
		tr.Do(req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("context cancel did not abort backoff")
	}
}

func TestNoRetryOnSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	if _, err := tr.Do(postReq(t, srv.URL, `{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected exactly 1 hit, got %d", hits)
	}
}
