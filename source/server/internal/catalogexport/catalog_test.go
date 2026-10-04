package catalogexport

import (
	"bytes"
	"cercano/source/server/pkg/config"
	"encoding/json"
	"os"
	"testing"
)

func TestSnapshotAndSubscriptionIdentity(t *testing.T) {
	raw, err := Build("../..")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile("../../../enterpriseapi/catalog/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, saved) {
		t.Fatal("stale catalog: run go run ./cmd/export-enterprise-catalog from source/server")
	}
	var d Document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	providers := map[string]Provider{}
	for _, p := range d.Providers {
		providers[p.ID] = p
	}
	subscription := providers["openai-responses"]
	if subscription.Endpoint != "https://chatgpt.com/backend-api/codex" || subscription.Authentication != "subscription" {
		t.Fatalf("wrong subscription identity: %+v", subscription)
	}
	if providers["openai"].Endpoint == subscription.Endpoint {
		t.Fatal("API key and subscription routes collapsed")
	}
	for _, m := range subscription.Models {
		if m.ID == "gpt-5-mini" {
			t.Fatal("API-only model leaked into subscription suggestions")
		}
	}
	if providers["anthropic-subscription"].Authentication != "subscription" || providers["anthropic"].Authentication != "api_key" {
		t.Fatal("lost Claude access methods")
	}
	if providers["bedrock"].Status != "coming_soon" {
		t.Fatal("lost client availability annotation")
	}
	if len(providers["ollama"].Models) != 0 || providers["ollama"].Endpoint != "" {
		t.Fatal("invented Ollama inventory")
	}
	if providers["llama_server"].Endpoint != "" || len(providers["llama_server"].Models) == 0 {
		t.Fatal("local catalog must preserve models without inventing a host port")
	}
	tasks := config.TaskDefinitions()
	if len(tasks) != len(d.Tasks) {
		t.Fatal("missing task")
	}
	for i, task := range tasks {
		got := d.Tasks[i]
		if got.ID != string(task.Task) || got.Label != task.Label || got.Destination != string(task.Default.Destination) || got.Quality != string(task.Default.Quality) {
			t.Fatalf("task drift: %+v", got)
		}
	}
}
