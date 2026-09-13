package profilechain

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

type accountingProfileFixture struct{}

func (*accountingProfileFixture) Name() string { return "fixture" }
func (*accountingProfileFixture) Capabilities() inference.Capabilities {
	return inference.Capabilities{}
}
func (p *accountingProfileFixture) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	a := usage.StartAttempt(ctx, p.Name(), req.Model)
	response := llm.ChatResponse{Model: "served", Usage: llm.TokenUsage{Input: llm.ReportedTokens(11), Output: llm.ReportedTokens(7)}}
	a.FinishResponse(response, nil)
	return response, nil
}
func (*accountingProfileFixture) StreamChat(context.Context, inference.Call) (inference.Stream, error) {
	panic("unused by fixture")
}
func TestPhysicalAttemptSeesSelectedProfile(t *testing.T) {
	var last usage.AttemptObservation
	ctx := usage.WithAttempts(t.Context(), func(a usage.AttemptObservation) bool { last = a; return true }, usage.Attribution{})
	provider := &routeProvider{Provider: &accountingProfileFixture{}, profile: "selected-profile", destination: "secondary"}
	response, err := provider.Chat(ctx, inference.Call{Model: "requested"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Route.Profile != "selected-profile" {
		t.Fatal("fixture did not select a profile")
	}
	if last.Profile != "selected-profile" || last.Destination != "secondary" {
		t.Fatalf("physical attempt missed known route: %+v", last)
	}
	if last.Model != "served" || last.Tokens.Input.Value != 11 {
		t.Fatalf("physical route/usage changed: %+v", last)
	}
}
