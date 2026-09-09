package cloudfactory

import (
	"testing"

	"cercano/source/server/pkg/config"
)

// An OpenAI-compatible endpoint serves arbitrary models over one protocol. The
// client's vision capability must therefore come from the model, not from the
// fact that the transport can encode an image.
func TestChatCompletions_VisionRequiresConfirmedModel(t *testing.T) {
	p := config.CloudProfile{
		Name: "deepinfra", Flavor: FlavorChatCompletions,
		BaseURL: "https://api.deepinfra.com/v1/openai", Model: "openai/gpt-oss-120b",
	}
	prov, err := BuildCloudProvider(p, "key", Options{
		ModelSupportsVision: func(string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.Capabilities().SupportsVision {
		t.Fatal("unconfirmed model advertised vision support")
	}
}

func TestChatCompletions_ConfirmedModelKeepsVision(t *testing.T) {
	p := config.CloudProfile{
		Name: "deepinfra", Flavor: FlavorChatCompletions,
		BaseURL: "https://api.deepinfra.com/v1/openai", Model: "zai-org/GLM-5.3-Flash",
	}
	prov, err := BuildCloudProvider(p, "key", Options{
		ModelSupportsVision: func(model string) bool { return model == "zai-org/GLM-5.3-Flash" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !prov.Capabilities().SupportsVision {
		t.Fatal("confirmed model lost vision support")
	}
}

// A missing oracle must not grant the capability.
func TestChatCompletions_NoOracleMeansNoVision(t *testing.T) {
	p := config.CloudProfile{
		Name: "custom", Flavor: FlavorChatCompletions,
		BaseURL: "https://gateway.example.com/v1", Model: "mystery",
	}
	prov, err := BuildCloudProvider(p, "key")
	if err != nil {
		t.Fatal(err)
	}
	if prov.Capabilities().SupportsVision {
		t.Fatal("absent capability evidence granted vision support")
	}
}

// Anthropic's messages flavor is bound to one vendor whose current families
// are all multimodal; it keeps its fixed answer and must not regress.
func TestMessages_KeepsVendorVisionWithoutOracle(t *testing.T) {
	p := config.CloudProfile{
		Name: "anthropic", Flavor: FlavorMessages, Model: "claude-sonnet-4-6",
	}
	prov, err := BuildCloudProvider(p, "key")
	if err != nil {
		t.Fatal(err)
	}
	if !prov.Capabilities().SupportsVision {
		t.Fatal("anthropic messages client lost vision support")
	}
}
