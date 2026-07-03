package capabilities

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
)

type stubProvider struct{ name string }

func (p stubProvider) Name() string                  { return p.name }
func (p stubProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (p stubProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p stubProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func TestMainProviderSelectsByFlag(t *testing.T) {
	cloud := stubProvider{name: "cloud"}
	local := stubProvider{name: "local"}
	s := Services{CloudProvider: cloud, OpenProvider: local}
	if s.MainProvider(true).Name() != "cloud" {
		t.Fatal("isCloud=true should pick cloud provider")
	}
	if s.MainProvider(false).Name() != "local" {
		t.Fatal("isCloud=false should pick local provider")
	}
}

func TestMainProviderFallsBackToOpenWhenNoCloud(t *testing.T) {
	local := stubProvider{name: "local"}
	s := Services{OpenProvider: local}
	if s.MainProvider(true).Name() != "local" {
		t.Fatal("nil cloud should fall back to local")
	}
}
