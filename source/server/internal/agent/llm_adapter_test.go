package agent

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
)

type fakeLLM struct {
	name     string
	gotModel string
}

func (f *fakeLLM) Name() string                   { return f.name }
func (f *fakeLLM) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (f *fakeLLM) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.gotModel = req.Model
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
