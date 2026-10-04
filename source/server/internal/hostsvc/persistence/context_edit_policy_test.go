package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

type contextEditLocalFailure struct{ calls int }

func (p *contextEditLocalFailure) Name() string { return "personal-local" }
func (p *contextEditLocalFailure) Process(context.Context, *agent.Request) (*agent.Response, error) {
	p.calls++
	return nil, errors.New("personal local unavailable")
}

// Keep real HTTP adapters/authorization while supplying known fallback capacity.
type contextEditProvider struct{ inference.Provider }

func (p contextEditProvider) TargetFor(model, _ string) llm.ServingRoute {
	return llm.ServingRoute{Model: model, ContextWindow: 65536, ContextWindowKnown: true}
}

func TestContextEditPolicyRouting(t *testing.T) {
	for _, tc := range []struct {
		name          string
		response      string
		fallback      bool
		revoked       bool
		missingTask   bool
		missingScope  bool
		missingEngine bool
		standalone    bool
		want          []string
		wantError     bool
	}{
		{name: "locked compaction ignores personal cloud", want: []string{"approved"}},
		{name: "failure cannot use approved but unassigned cloud", response: "quota", want: []string{"approved"}, wantError: true},
		{name: "explicit fallback is used", response: "quota", fallback: true, want: []string{"approved", "backup"}},
		{name: "revoked primary is terminal", revoked: true, fallback: true, wantError: true},
		{name: "invalid output cannot use personal cloud", response: "invalid", want: []string{"approved"}, wantError: true},
		{name: "missing assignment fails closed", missingTask: true, wantError: true},
		{name: "lost scope fails closed", missingScope: true, wantError: true},
		{name: "missing engine fails closed", missingEngine: true, wantError: true},
		{name: "standalone retains local then cloud", standalone: true, want: []string{"personal"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var hits []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Model     string
					MaxTokens int `json:"max_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				hits = append(hits, wire.Model)
				mu.Unlock()
				if wire.MaxTokens != 1024 {
					t.Errorf("context edit output limit=%d, want 1024", wire.MaxTokens)
				}
				w.Header().Set("Content-Type", "application/json")
				if wire.Model == "approved" && tc.response == "quota" {
					w.WriteHeader(429)
					_, _ = w.Write([]byte(`{"error":{"message":"quota exhausted","type":"insufficient_quota","code":"insufficient_quota"}}`))
					return
				}
				content := `{"delete_ids":["a"],"rationale":"remove tangent"}`
				if tc.response == "invalid" {
					content = "not a proposal"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "fixture", "object": "chat.completion", "model": wire.Model,
					"choices": []any{map[string]any{
						"index": 0, "finish_reason": "stop",
						"message": map[string]string{"role": "assistant", "content": content},
					}},
				})
			}))
			defer server.Close()
			makeProvider := func(model string) inference.Provider {
				return contextEditProvider{openai.NewClient(openai.Config{BaseURL: server.URL + "/v1", Model: model})}
			}
			snapshot := settingstest.Snapshot("company", "1", "guidance")
			snapshot.Policy.AllowedRoutes = nil
			for _, model := range []string{"approved", "backup", "personal"} {
				snapshot.Policy.AllowedRoutes = append(snapshot.Policy.AllowedRoutes, v1.Route{ID: model, Provider: "openai", Endpoint: server.URL + "/v1", Model: model, Placement: "external"})
			}
			snapshot.Policy.TaskDefaults[0].Task = string(config.TaskCompaction)
			if tc.fallback {
				snapshot.Policy.TaskDefaults[0].FallbackRouteIDs = []string{"backup"}
			}
			if tc.missingTask {
				snapshot.Policy.TaskDefaults = nil
			}
			ctx := t.Context()
			if !tc.standalone {
				if !tc.missingScope {
					ctx = managedsettings.WithSnapshot(ctx, snapshot)
				}
				restore := modelpolicy.Install(modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
					if (tc.revoked && a.Model == "approved") || !modelpolicy.Allowed(snapshot.Policy, a) {
						return modelpolicy.Deny(a, "revoked")
					}
					return nil
				}))
				defer restore()
			}
			eng := dispatch.NewEngine(func() inference.Tiers {
				return inference.Tiers{Cloud: makeProvider("personal"), ManagedRoute: func(_ context.Context, r v1.Route, _ config.Destination) (inference.Candidate, error) {
					return inference.Candidate{Provider: makeProvider(r.Model), IsCloud: true}, nil
				}}
			}, func() locus.Mode { return locus.CloudPrimary }, nil)
			store, err := conversation.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.EnsureConversation(ctx, "conv", "/fixture", "personal"); err != nil {
				t.Fatal(err)
			}
			if err = store.Append(ctx, conversation.Turn{ID: "a", ConversationID: "conv", Role: "user", Content: "a tangent"}); err != nil {
				t.Fatal(err)
			}
			local := &contextEditLocalFailure{}
			s := &svc{
				convAgent:      viewportResumeFakeAgent{store: store},
				engine:         func() *dispatch.Engine { return eng },
				openTurnRunner: func() agent.TurnRunner { return local },
				cloudProvider:  func() inference.Provider { return makeProvider("personal") },
				cloudModel:     func() string { return "personal" },
			}
			if tc.missingEngine {
				s.engine = nil
			}
			got, err := s.ProposeContextEdit(ctx, &proto.ProposeContextEditRequest{ConversationId: "conv", Instruction: "drop tangent"})
			if (err != nil) != tc.wantError {
				t.Errorf("result=%v error=%v, want error=%v", got, err, tc.wantError)
			}
			if tc.revoked && !modelpolicy.IsDenial(err) {
				t.Errorf("policy denial lost its identity: %v", err)
			}
			if !tc.wantError && (got == nil || !reflect.DeepEqual(got.DeleteIds, []string{"a"})) {
				t.Errorf("unexpected proposal: %v", got)
			}
			mu.Lock()
			actual := append([]string(nil), hits...)
			mu.Unlock()
			if !reflect.DeepEqual(actual, tc.want) {
				t.Errorf("physical requests=%v, want %v", actual, tc.want)
			}
			if !tc.standalone && local.calls != 0 {
				t.Errorf("managed request used personal local provider %d times", local.calls)
			}
			turns, err := store.GetTurns(ctx, "conv")
			if err != nil || len(turns) != 1 {
				t.Fatalf("proposal mutated conversation: %v %v", turns, err)
			}
		})
	}
}
