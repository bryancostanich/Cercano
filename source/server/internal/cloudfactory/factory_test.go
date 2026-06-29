package cloudfactory

import (
	"testing"

	"cercano/source/server/pkg/config"
)

func TestBuildMessagesProvider(t *testing.T) {
	p, err := BuildCloudProvider(config.CloudProfile{Name: "c", Flavor: "messages", Model: "claude-x"}, "sk")
	if err != nil || p == nil || p.Name() != "anthropic" {
		t.Fatalf("messages → %v, %v", p, err)
	}
}

func TestBuildChatCompletionsProvider(t *testing.T) {
	p, err := BuildCloudProvider(config.CloudProfile{Name: "o", Flavor: "chat_completions", Model: "gpt-4o"}, "sk")
	if err != nil || p == nil || p.Name() != "openai" {
		t.Fatalf("chat_completions → %v, %v", p, err)
	}
}

func TestBuildUnsupportedFlavor(t *testing.T) {
	if _, err := BuildCloudProvider(config.CloudProfile{Name: "c", Flavor: ""}, "sk"); err == nil {
		t.Error("empty flavor should error")
	}
}

func TestBuildChatCompletionsWithBackend(t *testing.T) {
	p, err := BuildCloudProvider(config.CloudProfile{
		Name: "g", Flavor: "chat_completions", Backend: "gemini", Model: "gemini-2.5-flash",
	}, "sk")
	if err != nil || p == nil || p.Name() != "openai" {
		t.Fatalf("chat_completions+backend → %v, %v", p, err)
	}
}

func TestBuildResponsesProvider(t *testing.T) {
	p, err := BuildCloudProvider(config.CloudProfile{Name: "r", Flavor: "responses", Model: "gpt-5"}, "sk")
	if err != nil || p == nil || p.Name() != "openai-responses" {
		t.Fatalf("responses → %v, %v", p, err)
	}
}
