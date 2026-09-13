package anthropic

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

func TestAccountingStreamUsesReportedModelAndUnknownCustomProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"served-anthropic\",\"content\":[],\"usage\":{\"input_tokens\":11,\"output_tokens\":0,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()
	var last usage.AttemptObservation
	ctx := usage.WithAttempts(t.Context(), func(a usage.AttemptObservation) bool { last = a; return true }, usage.Attribution{})
	client := NewClient(Config{BaseURL: server.URL, APIKey: "fake"})
	reader, err := client.StreamChat(ctx, llm.ChatRequest{Model: "requested"})
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
	if last.Model != "served-anthropic" || last.Outcome != usage.Completed {
		t.Fatalf("reported stream model lost: %+v", last)
	}
	if last.Provider != "" {
		t.Fatalf("unidentified compatible endpoint mislabelled: %q", last.Provider)
	}
	if client.Name() != "anthropic" {
		t.Fatal("routing identity changed")
	}
}

func TestAccountingRecognizesNativeEndpoint(t *testing.T) {
	for _, base := range []string{"", "https://api.anthropic.com/"} {
		if got := NewClient(Config{BaseURL: base, APIKey: "fake"}).accountingProviderName(); got != "anthropic" {
			t.Fatalf("native provider=%q", got)
		}
	}
}
