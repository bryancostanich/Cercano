package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloseReleasesFullStreamProducer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		for i := 0; i < 100; i++ {
			if err := enc.Encode(map[string]any{"model": "fake", "message": map[string]string{"role": "assistant", "content": "x"}}); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, Model: "fake"})
	rd, err := c.StreamChat(t.Context(), ChatRequest{Model: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	r := rd.(*streamReader)
	// Fill the bounded consumer queue without reading it; the producer is then
	// blocked trying to deliver another event, not waiting on network input.
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for len(r.ch) < cap(r.ch) {
		select {
		case <-deadline:
			t.Fatal("stream did not fill")
		case <-ticker.C:
		}
	}
	rd.Close()
	select {
	case <-r.done:
	case <-time.After(100 * time.Millisecond):
		// Unblock the old implementation after observing its defect, avoiding a
		// leaked test goroutine even when this regression fails.
		go func() {
			for range r.ch {
			}
		}()
		t.Fatal("closed stream producer remained blocked on its full event queue")
	}
}
