package anthropic

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

func finalityProbeObservation(t *testing.T, finalZero bool) usage.AttemptObservation {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"served\",\"content\":[],\"usage\":{\"input_tokens\":11,\"output_tokens\":0,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}}\n\n")
		if finalZero {
			fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":0}}\n\n")
		}
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	var last usage.AttemptObservation
	ctx := usage.WithAttempts(t.Context(), func(a usage.AttemptObservation) bool { last = a; return true }, usage.Attribution{})
	reader, err := NewClient(Config{BaseURL: server.URL, APIKey: "fake"}).StreamChat(ctx, llm.ChatRequest{Model: "requested"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for {
		_, ok, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	// Remove incidental identity and timing, not accounting evidence.
	last.ID = ""
	last.Revision = 0
	last.StartedAt = time.Time{}
	last.EndedAt = time.Time{}
	return last
}

func TestStreamFinalUsageEvidenceIsDistinguishable(t *testing.T) {
	initialOnly := finalityProbeObservation(t, false)
	finalZero := finalityProbeObservation(t, true)
	if initialOnly.Tokens.Final || initialOnly.Tokens.Complete() || !finalZero.Tokens.Final || !finalZero.Tokens.Complete() {
		t.Fatalf("incorrect finality: initial=%+v final=%+v", initialOnly.Tokens, finalZero.Tokens)
	}
	if initialOnly.Tokens.Input != finalZero.Tokens.Input || initialOnly.Tokens.Output != finalZero.Tokens.Output {
		t.Fatal("finality changed measured counts")
	}
	if reflect.DeepEqual(initialOnly, finalZero) {
		t.Fatalf("initial-only usage and explicitly final zero collapsed to identical evidence: %+v", initialOnly)
	}
}
