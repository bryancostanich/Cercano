package agent

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
)

type fakeLLM struct {
	name         string
	gotModel     string
	gotMaxTokens int
	gotTemp      *float64
}

func (f *fakeLLM) Name() string                   { return f.name }
func (f *fakeLLM) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (f *fakeLLM) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.gotModel = req.Model
	f.gotMaxTokens = req.MaxTokens
	f.gotTemp = req.Temperature
	return llm.ChatResponse{
		Blocks:       []llm.Block{{Type: llm.BlockText, Text: "echo:" + req.Messages[0].Blocks[0].Text}},
		InputTokens:  3,
		OutputTokens: 4,
	}, nil
}
func (f *fakeLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	panic("unused")
}

func TestLLMModelProviderProcess(t *testing.T) {
	mp := NewLLMModelProvider(&fakeLLM{name: "openai"}, "gpt-5")
	if mp.Name() != "openai" {
		t.Errorf("name = %q", mp.Name())
	}
	resp, err := mp.Process(context.Background(), &Request{Input: "hi"})
	if err != nil || resp.Output != "echo:hi" || resp.OutputTokens != 4 || resp.InputTokens != 3 {
		t.Fatalf("resp = %+v err=%v", resp, err)
	}
}

func TestLLMModelProviderProcessSetsMaxTokens(t *testing.T) {
	// A ChatRequest with MaxTokens 0 reaches the wire as max_tokens:0, which
	// the Anthropic subscription endpoint answers with a zero-token completion
	// and no error — the silent-empty-output trap that gutted compaction
	// summaries. The one-shot adapter must always send a real budget.
	fake := &fakeLLM{name: "anthropic"}
	mp := NewLLMModelProvider(fake, "claude-opus-4-8")
	if _, err := mp.Process(context.Background(), &Request{Input: "hi"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.gotMaxTokens <= 0 {
		t.Fatalf("Chat received MaxTokens = %d, want > 0", fake.gotMaxTokens)
	}
}

func TestLLMModelProviderProcessThreadsTemperature(t *testing.T) {
	// Greedy decoding (temperature 0) is a correctness requirement for the
	// compaction summarizer's cloud fallback — default-temperature summaries
	// are a format coin flip (compaction-bakeoff-findings.md). Temperature is
	// a pointer end-to-end so 0 ("greedy") is distinguishable from unset
	// ("provider default").
	fake := &fakeLLM{name: "anthropic"}
	mp := NewLLMModelProvider(fake, "claude-opus-4-8")

	zero := 0.0
	if _, err := mp.Process(context.Background(), &Request{Input: "hi", Temperature: &zero}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.gotTemp == nil || *fake.gotTemp != 0 {
		t.Fatalf("Chat received Temperature = %v, want explicit 0", fake.gotTemp)
	}

	if _, err := mp.Process(context.Background(), &Request{Input: "hi"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.gotTemp != nil {
		t.Fatalf("unset Temperature must stay nil (provider default), got %v", *fake.gotTemp)
	}
}

func TestLLMModelProviderProcessModelOverride(t *testing.T) {
	fake := &fakeLLM{name: "openai"}
	mp := NewLLMModelProvider(fake, "gpt-5")
	resp, err := mp.Process(context.Background(), &Request{Input: "hi", ModelOverride: "gpt-override"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.gotModel != "gpt-override" {
		t.Errorf("Chat received model = %q, want %q", fake.gotModel, "gpt-override")
	}
	if resp.RoutingMetadata.ModelName != "gpt-override" {
		t.Errorf("RoutingMetadata.ModelName = %q, want %q", resp.RoutingMetadata.ModelName, "gpt-override")
	}
}
