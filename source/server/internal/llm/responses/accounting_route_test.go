package responses

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
	accounting "cercano/source/server/internal/usage"
)

func TestAccountingStreamUsesReportedModelAndUnknownCustomProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"model\":\"served-responses\",\"status\":\"in_progress\",\"output\":[]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"model\":\"served-responses\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":11,\"output_tokens\":7}}}\n\n"))
	}))
	defer server.Close()
	var last accounting.AttemptObservation
	ctx := accounting.WithAttempts(t.Context(), func(a accounting.AttemptObservation) bool { last = a; return true }, accounting.Attribution{})
	client := NewClient(Config{BaseURL: server.URL, APIKey: "fake", Model: "requested"})
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
	if last.Model != "served-responses" || last.Outcome != accounting.Completed {
		t.Fatalf("reported stream model lost: %+v", last)
	}
	if last.Provider != "" {
		t.Fatalf("unidentified compatible endpoint mislabelled: %q", last.Provider)
	}
	if client.Name() != "openai-responses" {
		t.Fatal("routing identity changed")
	}
}

func TestAccountingRecognizesNativeEndpoints(t *testing.T) {
	for _, cfg := range []Config{{APIKey: "fake"}, {BaseURL: defaultBaseURL + "/", APIKey: "fake"}, {Route: RouteChatGPT}} {
		if got := NewClient(cfg).accountingProviderName(); got != "openai-responses" {
			t.Fatalf("native provider=%q", got)
		}
	}
}

// A custom Responses-compatible endpoint with a named cloud profile must
// attribute attempts to the profile name; official endpoints keep their
// canonical identity regardless of profile.
func TestAccountingFallsBackToProfileNameForCustomEndpoint(t *testing.T) {
	if got := NewClient(Config{BaseURL: "https://example.com/v1", APIKey: "k", AccountingProfile: "my-relay"}).accountingProviderName(); got != "my-relay" {
		t.Fatalf("accountingProviderName=%q want profile name %q", got, "my-relay")
	}
	if got := NewClient(Config{BaseURL: "https://example.com/v1", APIKey: "k"}).accountingProviderName(); got != "" {
		t.Fatalf("accountingProviderName=%q want unknown without profile", got)
	}
	official := NewClient(Config{APIKey: "k", AccountingProfile: "my-openai"})
	if got := official.accountingProviderName(); got == "my-openai" || got == "" {
		t.Fatalf("accountingProviderName=%q want canonical identity for official endpoint", got)
	}
}
