// Package catalogexport builds public editor metadata from the client's own
// catalogs. It reads no user configuration, credentials or network services.
package catalogexport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"cercano/source/server/internal/cloudcatalog"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/pkg/config"
)

type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type Model struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Qualities []string `json:"qualities,omitempty"`
}
type Provider struct {
	ID             string  `json:"id"`
	Label          string  `json:"label"`
	Provider       string  `json:"provider"`
	Endpoint       string  `json:"endpoint"`
	Placement      string  `json:"placement"`
	Authentication string  `json:"authentication"`
	Status         string  `json:"status"`
	Models         []Model `json:"models"`
}
type Task struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Destination string `json:"destination"`
	Quality     string `json:"quality"`
}
type Document struct {
	Version      int        `json:"version"`
	Providers    []Provider `json:"providers"`
	Destinations []Choice   `json:"destinations"`
	Qualities    []Choice   `json:"qualities"`
	Tasks        []Task     `json:"tasks"`
}

// Build expects the source/server directory. Only curated repository files are
// read; Defaults deliberately bypasses environment/user configuration loading.
func Build(serverDir string) ([]byte, error) {
	d := Document{Version: 1}
	defaults := config.Defaults()
	recommendations, err := config.LoadTierRecommendations()
	if err != nil {
		return nil, err
	}
	for _, dest := range []config.Destination{config.DestinationPrimary, config.DestinationSecondary, config.DestinationLocal} {
		d.Destinations = append(d.Destinations, Choice{string(dest), dest.Label()})
	}
	for _, q := range []config.CostTier{config.CostEconomy, config.CostStandard, config.CostPremium} {
		d.Qualities = append(d.Qualities, Choice{string(q), q.Label()})
	}
	for _, t := range config.TaskDefinitions() {
		d.Tasks = append(d.Tasks, Task{string(t.Task), t.Label, string(t.Default.Destination), string(t.Default.Quality)})
	}
	for _, p := range cloudcatalog.Catalog() {
		profile := config.CloudProfile{Flavor: p.Flavor, Backend: p.Backend, BaseURL: p.BaseURL, Route: p.Route}
		// This catalog entry represents subscription sign-in; the login handler
		// supplies the route even though the existing preset omits it.
		if p.ID == "openai-responses" {
			profile.Route = cloudfactory.RouteChatGPT
		}
		provider, endpoint, err := cloudfactory.PhysicalEndpoint(context.Background(), profile)
		if err != nil && p.Tier != cloudcatalog.TierComingSoon {
			return nil, err
		}
		auth := "api_key"
		if cloudfactory.IsSubscription(profile) {
			auth = "subscription"
		}
		if p.Flavor == cloudfactory.FlavorBedrock {
			auth = "aws"
		}
		out := Provider{ID: p.ID, Label: p.Label, Provider: provider, Endpoint: endpoint, Placement: "external", Authentication: auth, Status: string(p.Tier), Models: []Model{}}
		if err == nil {
			for _, q := range []config.CostTier{config.CostEconomy, config.CostStandard, config.CostPremium} {
				id := defaults.ModelProfiles.ResolveCloudModelForTier(profile, q.CapabilityTier())
				if id == "" {
					continue
				}
				found := false
				for i := range out.Models {
					if out.Models[i].ID == id {
						out.Models[i].Qualities = append(out.Models[i].Qualities, string(q))
						found = true
					}
				}
				if !found {
					out.Models = append(out.Models, Model{ID: id, Label: id, Qualities: []string{string(q)}})
				}
			}
		}
		// Setup-wizard candidates add vendor-specific alternatives. Never use
		// direct-API recommendations for ChatGPT's narrower subscription route.
		key := p.ID
		if key == "anthropic-subscription" {
			key = "anthropic"
		}
		for _, q := range []config.CostTier{config.CostEconomy, config.CostStandard, config.CostPremium} {
			for _, id := range recommendations.Candidates(config.ProviderCloud, key, q.CapabilityTier()) {
				addModel(&out, Model{ID: id, Label: id, Qualities: []string{string(q)}})
			}
		}
		if p.Flavor == cloudfactory.FlavorMessages {
			for _, m := range config.ClaudeModelChoices() {
				addModel(&out, Model{ID: m.ID, Label: m.DisplayName})
			}
		}

		d.Providers = append(d.Providers, out)
	}
	// Ollama's model tags come from the configured server, not the GGUF/UQFF
	// catalogs. Never relabel those incompatible IDs as Ollama models.
	// The default localhost hostname is not a signed-policy literal loopback
	// address; require the operator to supply the matching configured endpoint.
	d.Providers = append(d.Providers, Provider{ID: "ollama", Label: "Ollama", Provider: "ollama", Endpoint: "", Placement: "local", Authentication: "none", Status: "runtime", Models: []Model{}})
	for _, runtime := range []struct{ id, dir, label string }{{"llama_server", "llamaserver", "llama-server"}, {"mistralrs", "mistralrs", "mistral.rs"}} {
		raw, err := os.ReadFile(filepath.Join(serverDir, "internal", "localruntime", runtime.dir, "catalog.json"))
		if err != nil {
			return nil, err
		}
		var c struct {
			Models map[string]struct {
				Name        string `json:"display_name"`
				Status      string `json:"status"`
				PlainChatOK *bool  `json:"plain_chat_ok"`
				Tools       bool   `json:"supports_tools"`
			} `json:"models"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s catalog: %w", runtime.id, err)
		}
		out := Provider{ID: runtime.id, Label: runtime.label, Provider: runtime.id, Placement: "local", Authentication: "none", Status: "runtime", Models: []Model{}}
		ids := []string{}
		for id := range c.Models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			m := c.Models[id]
			if m.Status == "broken" || !m.Tools || (m.PlainChatOK != nil && !*m.PlainChatOK) {
				continue
			}
			out.Models = append(out.Models, Model{ID: id, Label: m.Name})
		}
		// Supervised sidecar ports are selected by the running host; no invented
		// universal endpoint is safe here. The editor requires the actual address.
		d.Providers = append(d.Providers, out)
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	return append(raw, '\n'), err
}

func addModel(p *Provider, model Model) {
	for i := range p.Models {
		if p.Models[i].ID != model.ID {
			continue
		}
		if model.Label != model.ID {
			p.Models[i].Label = model.Label
		}
		for _, q := range model.Qualities {
			found := false
			for _, old := range p.Models[i].Qualities {
				if old == q {
					found = true
				}
			}
			if !found {
				p.Models[i].Qualities = append(p.Models[i].Qualities, q)
			}
		}
		return
	}
	p.Models = append(p.Models, model)
}
