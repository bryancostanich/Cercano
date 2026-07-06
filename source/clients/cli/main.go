// Command cercano-cli is the standalone terminal client for Cercano.
//
// It is a thin gRPC client: it connects to a running cercano agent server on
// localhost:<port>, or auto-launches one (as `cercano agent`) if none is
// listening. The agent server is a singleton — multiple clients (this CLI, the
// VS Code extension, etc.) share the same server and conversation store.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	cliui "cercano/source/clients/cli/internal/ui"
	"cercano/source/clients/cli/internal/wizard"
	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/update"
)

// version is set at build time via -ldflags "-X main.version=...".
// Falls back to "dev" for local builds.
var version = "dev"

func init() {
	// Normalize: strip leading "v" so the print format "v%s" doesn't double up.
	version = strings.TrimPrefix(version, "v")
}

func main() {
	resumeShort := flag.Bool("r", false, "Open the conversation history picker on launch (alias for --resume)")
	resumeLong := flag.Bool("resume", false, "Open the conversation history picker on launch")
	setupShort := flag.Bool("s", false, "Open the setup wizard on launch (alias for --setup)")
	setupLong := flag.Bool("setup", false, "Open the setup wizard on launch")
	mdtest := flag.Bool("mdtest", false, "Launch the TUI with a markdown doc pre-loaded for render testing (optional file path as a positional arg; built-in sample if omitted)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("cercano-cli v%s\n", version)
		if info := update.CheckForUpdate(version); info != nil {
			if info.UpdateAvailable {
				fmt.Printf("\nA newer version is available: v%s\n", info.LatestVersion)
				fmt.Printf("  Upgrade: %s\n", info.UpgradeCommand())
				fmt.Printf("  Release: %s\n", info.ReleaseURL)
			} else {
				fmt.Println("(up to date)")
			}
		}
		return
	}

	// Config is shared with the server (pkg/config); the CLI needs the port to
	// dial the agent.
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to load config: %v (using defaults)\n", err)
		cfg = config.Defaults()
	}

	openHistory := *resumeShort || *resumeLong
	// The wizard opens on explicit request, on first run (no config file
	// yet), or when a previous run was left unfinished (resume).
	openWizard := *setupShort || *setupLong
	if !openWizard {
		if _, statErr := os.Stat(config.DefaultPath()); os.IsNotExist(statErr) {
			openWizard = true
		}
	}
	if !openWizard {
		_, openWizard = wizard.Load()
	}
	seedDoc := ""
	if *mdtest {
		// Render-testing mode: launch the TUI with a markdown doc pre-loaded.
		// No model round-trip — the doc renders through the normal viewport path.
		seedDoc = loadMdTestDoc(flag.Arg(0))
	}
	runCLI(cfg, openHistory, openWizard, seedDoc)
}

// runCLI launches the cercano TUI. Connects to a running agent on
// localhost:<cfg.Port>, or auto-launches one (as `cercano agent`) on miss.
// openHistoryOnStart opens the /history picker immediately after first paint
// (used by the -r / --resume flag).
func runCLI(cfg config.Config, openHistoryOnStart, openWizardOnStart bool, seedDoc string) {
	addr := "localhost:" + cfg.Port
	fmt.Fprintln(os.Stderr, "cercano: connecting to", addr+"…")
	ag, err := agentclient.Dial(context.Background(), addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cercano:", err)
		os.Exit(1)
	}
	defer ag.Close()
	if ag.AutoLaunched {
		fmt.Fprintln(os.Stderr, "cercano: auto-launched agent server (log:", ag.ServerLog+")")
	}

	m := cliui.New(ag, openHistoryOnStart)
	if openWizardOnStart {
		m = m.OpenWizardOnStart()
	}
	if seedDoc != "" {
		m = m.SeedAssistantMarkdown(seedDoc)
	}
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "cercano:", err)
		os.Exit(1)
	}
}
