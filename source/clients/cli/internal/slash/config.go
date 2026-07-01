package slash

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cercano/source/server/pkg/agentclient"
)

// RegisterConfig wires /config and /cloud against the supplied client.
// Both commands are algorithmic — parsing is prefix-match, dispatch routes
// directly to UpdateConfig / GetConfig RPCs with no LLM involvement.
func RegisterConfig(r *Registry, c *agentclient.Client) {
	r.Register(Command{
		Name: "config",
		Help: "View or update runtime config. Usage: /config [key value]. Keys: local-runtime, local-model, cloud-provider, cloud-model, cloud-api-key, cloud-base-url, ollama-url, elide-tool-results, lossy-tool-elision, raw-retention-days, compacted-retention-days, keep-forever.",
		Handler: func(args []string) Result {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if len(args) == 0 {
				// Open the interactive editor.
				return Result{Kind: ResultOpenSettings}
			}
			// One-arg form: `/config show` prints current state without
			// opening the editor (handy for piping or scripted use).
			if len(args) == 1 && args[0] == "show" {
				cfg, err := c.GetConfig(ctx)
				if err != nil {
					return Result{Kind: ResultText, Text: "config: " + err.Error()}
				}
				return Result{Kind: ResultText, Text: formatConfig(cfg)}
			}
			if len(args) < 2 {
				return Result{Kind: ResultText, Text: "usage: /config <key> <value>, /config show, or /config alone for the editor"}
			}
			key := args[0]
			value := strings.Join(args[1:], " ")

			var update agentclient.ConfigUpdate
			switch key {
			case "local-runtime", "local_runtime":
				update.LocalRuntime = value
			case "local-model", "local_model":
				update.LocalModel = value
			case "ollama-url", "ollama_url":
				update.OllamaURL = value
			case "cloud-provider", "cloud_provider":
				update.CloudProvider = value
			case "cloud-model", "cloud_model":
				update.CloudModel = value
			case "cloud-api-key", "cloud_api_key":
				update.CloudAPIKey = value
			case "cloud-base-url", "cloud_base_url":
				update.CloudBaseURL = value
			case "elide-tool-results", "elide_tool_results":
				update.ElideToolResults = value
			case "lossy-tool-elision", "lossy_tool_elision":
				update.LossyToolElision = value
			case "raw-retention-days", "raw_retention_days":
				update.RawRetentionDays = value
			case "compacted-retention-days", "compacted_retention_days":
				update.CompactedRetentionDays = value
			case "keep-forever", "keep_forever":
				update.KeepForever = value
			default:
				return Result{Kind: ResultText, Text: "unknown config key /" + key + " (valid: local-runtime, local-model, ollama-url, cloud-provider, cloud-model, cloud-api-key, cloud-base-url, elide-tool-results, lossy-tool-elision, raw-retention-days, compacted-retention-days, keep-forever)"}
			}
			msg, err := c.UpdateConfig(ctx, update)
			if err != nil {
				return Result{Kind: ResultText, Text: "config update failed: " + err.Error()}
			}
			return Result{Kind: ResultText, Text: msg}
		},
	})

	r.Register(Command{
		Name: "cloud",
		Help: "Manage cloud profiles. Usage: /cloud [list] | /cloud use <name> | /cloud key <name> <api-key>.",
		Handler: func(args []string) Result {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			sub := ""
			if len(args) > 0 {
				sub = args[0]
			}

			switch sub {
			case "", "list":
				profiles, active, err := c.GetCloudProfiles(ctx)
				if err != nil {
					return Result{Kind: ResultText, Text: "cloud list: " + err.Error()}
				}
				return Result{Kind: ResultText, Text: formatCloudProfiles(profiles, active)}

			case "use":
				if len(args) < 2 {
					return Result{Kind: ResultText, Text: "usage: /cloud use <name>"}
				}
				name := args[1]
				if err := c.SetActiveCloudProfile(ctx, name); err != nil {
					return Result{Kind: ResultText, Text: "cloud use: " + err.Error()}
				}
				return Result{Kind: ResultText, Text: "active profile set to " + name}

			case "key":
				if len(args) < 3 {
					return Result{Kind: ResultText, Text: "usage: /cloud key <name> <api-key>"}
				}
				name := args[1]
				key := strings.Join(args[2:], " ")
				if err := c.SetCloudProfileKey(ctx, name, key); err != nil {
					return Result{Kind: ResultText, Text: "cloud key: " + err.Error()}
				}
				return Result{Kind: ResultText, Text: "key stored for profile " + name + " (" + maskKey(key) + ")"}

			default:
				return Result{Kind: ResultText, Text: "unknown subcommand " + sub + ". Usage: /cloud [list] | /cloud use <name> | /cloud key <name> <api-key>"}
			}
		},
	})
}

func formatConfig(cfg *agentclient.Config) string {
	var b strings.Builder
	b.WriteString("current config:\n")
	b.WriteString("  local-runtime:  ")
	b.WriteString(orDash(cfg.LocalRuntime))
	b.WriteString("\n  ollama-url:      ")
	b.WriteString(orDash(cfg.OllamaURL))
	b.WriteString("\n  local-model:     ")
	b.WriteString(orDash(cfg.LocalModel))
	b.WriteString("\n  embedding-model: ")
	b.WriteString(orDash(cfg.EmbeddingModel))
	b.WriteString("\n  cloud-provider:  ")
	b.WriteString(orDash(cfg.CloudProvider))
	b.WriteString("\n  cloud-model:     ")
	b.WriteString(orDash(cfg.CloudModel))
	b.WriteString("\n  cloud-base-url:  ")
	b.WriteString(orDash(cfg.CloudBaseURL))
	b.WriteString("\n  cloud-api-key:   ")
	if cfg.CloudAPIKeySet {
		b.WriteString("(set)")
	} else {
		b.WriteString("(unset)")
	}
	b.WriteString("\n  cloud-state:     ")
	b.WriteString(cfg.CloudState)
	b.WriteString("\n  port:            ")
	b.WriteString(orDash(cfg.Port))
	b.WriteString("\n  elide-tool-results: ")
	if cfg.ElideToolResults {
		b.WriteString("on")
	} else {
		b.WriteString("off")
	}
	b.WriteString("\n  lossy-tool-elision: ")
	if cfg.LossyToolElision {
		b.WriteString("on")
	} else {
		b.WriteString("off")
	}
	fmt.Fprintf(&b, "\n  raw-retention-days: %d", cfg.RawRetentionDays)
	fmt.Fprintf(&b, "\n  compacted-retention-days: %d", cfg.CompactedRetentionDays)
	b.WriteString("\n  keep-forever: ")
	if cfg.KeepForever {
		b.WriteString("on")
	} else {
		b.WriteString("off")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// formatCloudProfiles renders a compact table of cloud profiles.
// active is the currently selected profile name; an asterisk marks it.
func formatCloudProfiles(profiles []agentclient.CloudProfileInfo, active string) string {
	if len(profiles) == 0 {
		return "cloud: no profiles configured"
	}

	// Column widths: name, flavor, model — compute from data.
	wName, wFlavor, wModel := len("name"), len("flavor"), len("model")
	for _, p := range profiles {
		if n := len(p.Name); n > wName {
			wName = n
		}
		if n := len(p.Flavor); n > wFlavor {
			wFlavor = n
		}
		if n := len(p.Model); n > wModel {
			wModel = n
		}
	}

	var b strings.Builder
	// Header.
	b.WriteString(fmt.Sprintf("%-*s  %-*s  %-*s  active  key\n",
		wName, "name", wFlavor, "flavor", wModel, "model"))
	for _, p := range profiles {
		activeMarker := " "
		if p.Name == active {
			activeMarker = "*"
		}
		keyMarker := "✗"
		if p.HasKey {
			keyMarker = "✓"
		}
		b.WriteString(fmt.Sprintf("%-*s  %-*s  %-*s  %-6s  %s\n",
			wName, p.Name, wFlavor, p.Flavor, wModel, p.Model, activeMarker, keyMarker))
	}
	return strings.TrimRight(b.String(), "\n")
}

// maskKey returns a masked representation of an API key safe to display.
// Shows first 4 characters followed by ellipsis, or just "***" for short keys.
func maskKey(key string) string {
	if len(key) <= 4 {
		return "***"
	}
	return key[:4] + "..."
}
