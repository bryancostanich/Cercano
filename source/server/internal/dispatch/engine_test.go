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

func TestOneShotReturnsTextAndTokens(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
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
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
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
	eng := NewEngine(provs(Providers{Local: prov}), func() locus.Mode { return locus.LocalOnly }, nil)
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
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })
	eng.SetUsageSink(sink)

	_, err := eng.Dispatch(context.Background(), Spec{
		Mode:   OneShot,
		Role:   RoleCoproc,
		Prompt: "summarize this",
		Source: "coproc:summarize",
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
		t.Errorf("IsCloud=true, want false for LocalOnly locus")
	}
	if u.InputTokens != 9 || u.OutputTokens != 4 {
		t.Errorf("tokens=%d/%d, want 9/4", u.InputTokens, u.OutputTokens)
	}
}

func TestOneShotWantsProjectContext_NoContextFile(t *testing.T) {
	// Use a temp dir that has no .cercano/context.md — PrependContext returns prompt unchanged.
	prov := echoProvider{}
	loader := projectctx.NewLoader()
	eng := NewEngine(
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
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
