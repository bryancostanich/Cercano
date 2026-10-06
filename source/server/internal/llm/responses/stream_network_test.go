package responses

import (
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
	"golang.org/x/net/http2"
)

// errReader delivers buffered bytes, then fails with a fixed error on every
// subsequent read.
type resetReader struct {
	data string
	err  error
}

func (r *resetReader) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, r.err
}

func (r *resetReader) Close() error { return nil }

// TestStreamReadHTTP2PeerResetClassifiesNetwork pins the adapter seam from
// responses/stream.go Next(): a mid-stream read failure that is an HTTP/2
// peer reset (the 2026-10-01 incident's "stream error: stream ID 9;
// INTERNAL_ERROR; received from peer") must classify as the transient
// ErrNetwork with the provider attributed — not escape raw as ErrUnknown.
// golang.org/x/net's exported StreamError renders the identical shape to std
// net/http's bundled (unexported) http2StreamError, asserted in
// internal/llm/http2_peer_reset_test.go.
func TestStreamReadHTTP2PeerResetClassifiesNetwork(t *testing.T) {
	reset := http2.StreamError{StreamID: 9, Code: http2.ErrCodeInternal, Cause: errors.New("received from peer")}
	if got, want := reset.Error(), "stream error: stream ID 9; INTERNAL_ERROR; received from peer"; got != want {
		t.Fatalf("error shape = %q, want %q", got, want)
	}

	sr := newStreamReader(&resetReader{data: "data: {\"type\":\"response.created\"", err: reset}, "openai-responses")
	var last error
	for {
		_, ok, err := sr.Next()
		if err == nil && ok {
			continue
		}
		last = err
		break
	}
	if last == nil {
		t.Fatal("Next returned nil error, want the classified reset")
	}
	var le *llm.Error
	if !errors.As(last, &le) {
		t.Fatalf("Next returned %T (%v), want a normalized *llm.Error", last, last)
	}
	if le.Class != llm.ErrNetwork {
		t.Errorf("Class = %q, want %q", le.Class, llm.ErrNetwork)
	}
	if le.Provider != "openai-responses" {
		t.Errorf("Provider = %q, want %q", le.Provider, "openai-responses")
	}
	if !strings.Contains(last.Error(), "stream ID 9") {
		t.Errorf("vendor detail lost: %v", last)
	}
}
