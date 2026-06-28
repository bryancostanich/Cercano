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

func TestBuildUnsupportedFlavor(t *testing.T) {
	if _, err := BuildCloudProvider(config.CloudProfile{Name: "c", Flavor: "chat_completions"}, "sk"); err == nil {
		t.Error("chat_completions should be unsupported in the foundation")
	}
	if _, err := BuildCloudProvider(config.CloudProfile{Name: "c", Flavor: ""}, "sk"); err == nil {
		t.Error("empty flavor should error")
	}
}
