package dispatch

import (
	"context"
	"strings"
	"testing"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/usage"
)

// provs adapts a static Providers value to the func() Providers that NewEngine takes.
func provs(p Providers) func() Providers { return func() Providers { return p } }

// echoProvider implements llm.Provider for testing.
// Chat echoes the last user message text back with a prefix and fixed token counts.
type echoProvider struct{}

func (echoProvider) Name() string                   { return "echo" }
func (echoProvider) Capabilities() llm.Capabilities { return llm.Capabilities{SupportsTools: true} }
func (echoProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	last := ""
	if n := len(req.Messages); n > 0 {
		for _, b := range req.Messages[n-1].Blocks {
			if b.Type == llm.BlockText {
				last += b.Text
			}
		}
	}
	return llm.ChatResponse{
		Blocks:       []llm.Block{{Type: llm.BlockText, Text: "echo: " + last}},
		InputTokens:  9,
		OutputTokens: 4,
	}, nil
}
func (echoProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

// sessionEchoProvider records the llm session id each Chat ctx carried.
type sessionEchoProvider struct {
	echoProvider
	seen []string
}

func (p *sessionEchoProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.seen = append(p.seen, llm.SessionIDFromContext(ctx))
	return p.echoProvider.Chat(ctx, req)
}

// A one-shot dispatch is not part of the calling conversation — its provider
// call must not inherit the caller's session identity (upstream bridges key
// persistent session state on it; a one-message history arriving on the
// parent's key evicts the parent's lineage). Each one-shot gets a fresh id.
func TestOneShotScopesOwnProviderSession(t *testing.T) {
	prov := &sessionEchoProvider{}
	eng := NewEngine(
		provs(Providers{Open: prov}),
		func() locus.Mode { return locus.OpenOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })

	parentCtx := llm.WithSessionID(context.Background(), "parent-session-id")
	for i := 0; i < 2; i++ {
		if _, err := eng.Dispatch(parentCtx, Spec{
			Mode: OneShot, Role: RoleCoproc, Prompt: "summarize this", Source: "coproc:summarize",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if len(prov.seen) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(prov.seen))
	}
	for i, sid := range prov.seen {
		if sid == "parent-session-id" {
			t.Errorf("one-shot %d inherited the caller's session id", i)
		}
		if sid == "" {
			t.Errorf("one-shot %d carries no session id", i)
		}
	}
	if prov.seen[0] == prov.seen[1] {
		t.Errorf("one-shots must get distinct session ids, got %q twice", prov.seen[0])
	}
}

func TestOneShotReturnsTextAndTokens(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Open: prov}),
		func() locus.Mode { return locus.OpenOnly },
		nil, // no project context loader
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })

	res, err := eng.Dispatch(context.Background(), Spec{
		Mode: OneShot, Role: RoleCoproc, Prompt: "summarize this", Source: "coproc:summarize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "summarize this") {
		t.Fatalf("expected echoed prompt, got %q", res.Text)
	}
	if res.Model != "local-model" || res.IsCloud {
		t.Fatalf("locus_only=local must pick local model: %+v", res)
	}
	if res.InputTokens == 0 || res.OutputTokens == 0 {
		t.Fatalf("tokens not propagated: %+v", res)
	}
}

func TestOneShotModelOverride(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Open: prov}),
		func() locus.Mode { return locus.OpenOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "default-model" })

	res, err := eng.Dispatch(context.Background(), Spec{
		Mode:          OneShot,
		Role:          RoleCoproc,
		Prompt:        "hello",
		ModelOverride: "override-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "override-model" {
		t.Fatalf("expected override-model, got %q", res.Model)
	}
}

func TestOneShotAgenticReturnsError(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(provs(Providers{Open: prov}), func() locus.Mode { return locus.OpenOnly }, nil)
	// No AgenticRunner installed — must return a clear error.

	_, err := eng.Dispatch(context.Background(), Spec{Mode: Agentic, Role: RoleCoproc, Task: "x"})
	if err == nil {
		t.Fatal("expected error for Agentic mode with no runner")
	}
	if !strings.Contains(err.Error(), "agentic runner not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOneShotEmitsSourceLabeledUsage(t *testing.T) {
	var captured []usage.Usage
	sink := func(u usage.Usage) { captured = append(captured, u) }

	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Open: prov}),
		func() locus.Mode { return locus.OpenOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })
	eng.SetUsageSink(sink)

	_, err := eng.Dispatch(context.Background(), Spec{
		Mode:        OneShot,
		Role:        RoleCoproc,
		Prompt:      "summarize this",
		Source:      "coproc:summarize",
		RecordUsage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected exactly 1 usage event, got %d", len(captured))
	}
	u := captured[0]
	if u.Source != "coproc:summarize" {
		t.Errorf("Source=%q, want %q", u.Source, "coproc:summarize")
	}
	if u.Model != "local-model" {
		t.Errorf("Model=%q, want %q", u.Model, "local-model")
	}
	if u.IsCloud {
		t.Errorf("IsCloud=true, want false for OpenOnly locus")
	}
	if u.InputTokens != 9 || u.OutputTokens != 4 {
		t.Errorf("tokens=%d/%d, want 9/4", u.InputTokens, u.OutputTokens)
	}
}

func TestOneShotRecordsUsageOnlyWhenRequested(t *testing.T) {
	var captured []usage.Usage
	sink := func(u usage.Usage) { captured = append(captured, u) }

	prov := echoProvider{}
	mkEng := func() *Engine {
		eng := NewEngine(
			provs(Providers{Open: prov}),
			func() locus.Mode { return locus.OpenOnly },
			nil,
		)
		eng.SetModelFor(func(isCloud bool) string { return "local-model" })
		eng.SetUsageSink(sink)
		return eng
	}

	// Case 1: RecordUsage=true — must emit exactly one event with savings fields.
	captured = nil
	eng := mkEng()
	_, err := eng.Dispatch(context.Background(), Spec{
		Mode:                 OneShot,
		Role:                 RoleCoproc,
		Prompt:               "do something",
		Source:               "summarize",
		RecordUsage:          true,
		ContentTokensAvoided: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 {
		t.Fatalf("RecordUsage=true: expected 1 usage event, got %d", len(captured))
	}
	u := captured[0]
	if u.Source != "summarize" {
		t.Errorf("Source=%q, want %q", u.Source, "summarize")
	}
	if u.ContentTokensAvoided != 42 {
		t.Errorf("ContentTokensAvoided=%d, want 42", u.ContentTokensAvoided)
	}
	if !u.TokenSaving {
		t.Errorf("TokenSaving=false, want true")
	}
	if u.InputTokens != 9 || u.OutputTokens != 4 {
		t.Errorf("tokens=%d/%d, want 9/4", u.InputTokens, u.OutputTokens)
	}

	// Case 2: RecordUsage=false — must emit nothing.
	captured = nil
	eng2 := mkEng()
	_, err = eng2.Dispatch(context.Background(), Spec{
		Mode:        OneShot,
		Role:        RoleCoproc,
		Prompt:      "do something",
		Source:      "coproc:research",
		RecordUsage: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Fatalf("RecordUsage=false: expected 0 usage events, got %d", len(captured))
	}
}

func TestOneShotWantsProjectContext_NoContextFile(t *testing.T) {
	// Use a temp dir that has no .cercano/context.md — PrependContext returns prompt unchanged.
	prov := echoProvider{}
	loader := projectctx.NewLoader()
	eng := NewEngine(
		provs(Providers{Open: prov}),
		func() locus.Mode { return locus.OpenOnly },
		loader,
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })

	res, err := eng.Dispatch(context.Background(), Spec{
		Mode:                OneShot,
		Role:                RoleCoproc,
		Prompt:              "explain this",
		WantsProjectContext: true,
		WorkDir:             t.TempDir(), // empty dir, no .cercano/context.md
	})
	if err != nil {
		t.Fatal(err)
	}
	// echoProvider echoes the prompt; since no context file exists, prompt is unchanged.
	if !strings.Contains(res.Text, "explain this") {
		t.Fatalf("expected prompt in response, got %q", res.Text)
	}
}
