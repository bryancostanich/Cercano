package runner

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

// localStartupRefusalProvider models the llama-server memory guard refusing to
// start: the failure happens before any request is submitted.
type localStartupRefusalProvider struct{}

func (p *localStartupRefusalProvider) Name() string { return "llama_server" }
func (p *localStartupRefusalProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *localStartupRefusalProvider) startupErr() error {
	return &llm.LocalStartupError{Provider: "llama_server", Model: "glm-local", Err: errors.New("memory guard refused startup")}
}
func (p *localStartupRefusalProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, p.startupErr()
}
func (p *localStartupRefusalProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, p.startupErr()
}

// A main-agent turn must not die because the local runtime refused to start:
// locus permits the other tier, so the turn continues there.
func TestCore_LocalStartupRefusalFallsBackCrossTier(t *testing.T) {
	cloudSpy := &spyProvider{}
	deps := buildDeps(&localStartupRefusalProvider{})
	deps.Config = &fakeConfig{cfg: config.Config{LocusMode: "open_primary", OpenRuntime: "llama_server"}}
	deps.Providers = &fakeResolver{
		prov:       &localStartupRefusalProvider{},
		cloud:      cloudSpy,
		isCloud:    false,
		isCloudSet: true,
		openModel:  "glm-local",
		cloudModel: "claude-sonnet-4-6",
	}
	core := New(deps)

	_, err := core.RunTurn(context.Background(), Request{
		Input:          "survive a local startup refusal",
		ConversationID: "startup-refusal-conv",
		WorkDir:        t.TempDir(),
	}, noopSink{}, nil, nil)
	if err != nil {
		t.Fatalf("local startup refusal stranded the turn: %v", err)
	}
	cloudSpy.mu.Lock()
	defer cloudSpy.mu.Unlock()
	if len(cloudSpy.requests) == 0 {
		t.Fatal("expected cross-tier fallback provider to serve the turn")
	}
}
