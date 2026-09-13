package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

func TestAccountingUsesConfiguredBackendWithoutChangingRoutingName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"served","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`))
	}))
	defer server.Close()
	for _, backend := range []string{"llama_server", "mistralrs", "gemini", ""} {
		t.Run(backend, func(t *testing.T) {
			client := NewClient(Config{BaseURL: server.URL, Backend: backend, Model: "requested"})
			var observed []usage.AttemptObservation
			ctx := usage.WithAttempts(t.Context(), func(a usage.AttemptObservation) bool { observed = append(observed, a); return true }, usage.Attribution{})
			if _, err := client.Chat(ctx, llm.ChatRequest{}); err != nil {
				t.Fatal(err)
			}
			last := observed[len(observed)-1]
			if last.Provider != backend {
				t.Fatalf("provider=%q want configured backend %q (unknown for unlabelled custom endpoint)", last.Provider, backend)
			}
			if client.Name() != "openai" {
				t.Fatal("accounting changed provider-selection identity")
			}
		})
	}
}
func TestAccountingRetainsReportedStreamingModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"model\":\"served-version\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"model\":\"served-version\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	var last usage.AttemptObservation
	ctx := usage.WithAttempts(t.Context(), func(a usage.AttemptObservation) bool { last = a; return true }, usage.Attribution{})
	client := NewClient(Config{BaseURL: server.URL, Backend: "openai", Model: "requested-alias"})
	reader, err := client.StreamChat(ctx, llm.ChatRequest{})
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
	if last.Model != "served-version" || last.Outcome != usage.Completed {
		t.Fatalf("stream route=%+v", last)
	}
}

func TestAccountingRecognizesNativeEndpoint(t *testing.T) {
	for _, base := range []string{"", "https://api.openai.com/v1/"} {
		if got := NewClient(Config{BaseURL: base, APIKey: "fake"}).accountingProvider; got != "openai" {
			t.Fatalf("native provider=%q", got)
		}
	}
}
