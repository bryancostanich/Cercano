package agent

import (
	"context"
	"testing"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

type queryPolicyProvider struct {
	fakeLLMProvider
	seen []bool
}

func (p *queryPolicyProvider) Chat(ctx context.Context, r llm.ChatRequest) (llm.ChatResponse, error) {
	p.seen = append(p.seen, r.DisableThinking)
	return p.fakeLLMProvider.Chat(ctx, r)
}

func TestCoprocQueryPolicyReachesProviderWithoutLeaking(t *testing.T) {
	p := &queryPolicyProvider{fakeLLMProvider: fakeLLMProvider{name: "fixture", out: "query"}}
	engine := dispatch.NewEngine(func() dispatch.Providers { return dispatch.Providers{Open: p} }, func() locus.Mode { return locus.OpenOnly }, nil)
	engine.SetModelFor(func(bool, config.Tier) string { return "model" })
	a := NewAgent(&fakeCoprocRouter{}, nil)
	a.SetDispatchEngine(engine)
	for _, disable := range []bool{true, false} {
		if _, err := a.ProcessRequest(context.Background(), &Request{Input: "question", Coproc: true, DisableThinking: disable}); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.seen) != 2 || !p.seen[0] || p.seen[1] {
		t.Fatalf("provider policies=%v", p.seen)
	}
}
