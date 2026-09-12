package builtins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/llm"
	openaiadapter "cercano/source/server/internal/llm/openai"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/web"
	"cercano/source/server/pkg/config"
)

type queryHTTPProvider struct{ *openaiadapter.Client }

func (*queryHTTPProvider) Name() string { return "llama_server" }

func (*queryHTTPProvider) RuntimeContext(context.Context, string, bool) (llm.RuntimeContext, error) {
	return llm.RuntimeContext{Window: 131072, InstanceID: "query-fixture"}, nil
}

type querySearchFixture struct{}

func (querySearchFixture) Search(context.Context, string, int) ([]web.SearchResult, error) {
	return []web.SearchResult{{Title: "Go embed", URL: "https://go.dev/doc/embed"}}, nil
}

type queryFetchFixture struct{}

func (queryFetchFixture) FetchURL(string) (*web.FetchResult, error) {
	return &web.FetchResult{Content: "Use //go:embed to embed a static file."}, nil
}

func TestResearchQueryPolicyThroughDispatchAndHTTP(t *testing.T) {
	observed := make(chan bool, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Template map[string]*bool `json:"chat_template_kwargs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		flag := body.Template["enable_thinking"]
		disabled := flag != nil && !*flag
		observed <- disabled
		answer := "Use //go:embed."
		if disabled {
			answer = "1. site:go.dev go embed"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	provider := &queryHTTPProvider{Client: openaiadapter.NewClient(openaiadapter.Config{Backend: "llama_server", Model: "fixture", BaseURL: server.URL + "/v1"})}
	routing := config.Config{TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskResearch: {Destination: config.DestinationLocal, Quality: config.CostPremium}}}
	engine := dispatch.NewEngine(func() dispatch.Providers { return dispatch.Providers{Open: provider, TaskFor: routing.TaskAssignment} }, func() locus.Mode { return locus.OpenOnly }, nil)
	engine.SetModelFor(func(bool, config.Tier) string { return "fixture" })
	caller := &dispatchModelCaller{call: &capabilities.Call{Svc: capabilities.Services{Dispatch: engine.Dispatch, DispatchTarget: engine.PreparedTarget}}, source: "research", tier: config.TierFastLightText}
	pipeline := web.NewResearchPipeline(caller, querySearchFixture{}, queryFetchFixture{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := pipeline.RunDurable(ctx, "What embeds files in Go?", 1, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Answer, "//go:embed") || len(result.Sources) != 1 {
		t.Fatalf("result=%+v", result)
	}
	for i, want := range []bool{true, false} {
		select {
		case got := <-observed:
			if got != want {
				t.Errorf("HTTP request %d disabled=%v want=%v", i, got, want)
			}
		default:
			t.Fatalf("missing HTTP request %d", i)
		}
	}
	select {
	case extra := <-observed:
		t.Fatalf("unexpected extra HTTP request: %v", extra)
	default:
	}
}
