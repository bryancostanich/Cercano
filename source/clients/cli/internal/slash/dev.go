package slash

import (
	"fmt"
	"os"
	"path/filepath"
)

// RegisterDev wires /d (alias /dev) — development mode: point the session's
// working directory at the Cercano repo and prime the agent to work on its
// own codebase. Repo resolution order: explicit argument, walk up from the
// current directory, then the CERCANO_REPO environment variable (which the
// generated dev launcher exports).
func RegisterDev(r *Registry) {
	r.Register(Command{
		Name:    "d",
		Aliases: []string{"dev"},
		Help:    "Development mode — work on the Cercano codebase itself. Usage: /d [repo-path]",
		Handler: func(args []string) Result {
			explicit := ""
			if len(args) > 0 {
				explicit = args[0]
			}
			cwd, _ := os.Getwd()
			repo, err := ResolveDevRepo(explicit, cwd, os.Getenv("CERCANO_REPO"))
			if err != nil {
				return Result{Kind: ResultText, Text: "dev mode: " + err.Error()}
			}
			return Result{Kind: ResultDevMode, WorkDir: repo}
		},
	})
}

// IsCercanoRepo reports whether dir is the root of a Cercano checkout. Two
// markers from the two separate Go modules make a false positive implausible;
// checking existence only keeps resolution purely algorithmic.
func IsCercanoRepo(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "source", "server", "cmd", "cercano"))
	if err != nil || !st.IsDir() {
		return false
	}
	st, err = os.Stat(filepath.Join(dir, "source", "clients", "cli", "main.go"))
	return err == nil && st.Mode().IsRegular()
}

// ResolveDevRepo resolves the Cercano repo root. A pure function of its
// inputs so tests don't have to manipulate the process cwd or environment:
// the handler passes os.Getwd() and os.Getenv("CERCANO_REPO").
func ResolveDevRepo(explicit, cwd, env string) (string, error) {
	if explicit != "" {
		p, err := filepath.Abs(explicit)
		if err == nil && IsCercanoRepo(p) {
			return p, nil
		}
		return "", fmt.Errorf("%s is not a Cercano repo root (needs source/server/cmd/cercano and source/clients/cli/main.go)", explicit)
	}
	dir := cwd
	for dir != "" {
		if IsCercanoRepo(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the filesystem root
			break
		}
		dir = parent
	}
	if env != "" {
		if absEnv, err := filepath.Abs(env); err == nil {
			env = absEnv
		}
		if IsCercanoRepo(env) {
			return env, nil
		}
	}
	return "", fmt.Errorf("could not find the Cercano repo — run from inside the repo, pass /d <path>, or set CERCANO_REPO (the generated launcher sets it for you)")
}

// DevKickoff returns the canned first prompt sent on entering dev mode. The
// docs are read live by the agent's own Read tool, so orientation always
// reflects current status — nothing baked in here can go stale except paths.
func DevKickoff(repo string) string {
	return fmt.Sprintf(`You are now in Cercano development mode: this session works on your own codebase, at %s. Before anything else, orient yourself by reading these three documents with your Read tool:

1. docs/features/cli/README.md — the CLI track: architecture, design principles, what's built, deviations from plan, and outstanding tasks.
2. docs/agent/README.md — the standalone agent: provider layer, tool loop, permission gating, persistence.
3. docs/agent/self-dev.md — how to build and test both modules, how to inspect your own logs and databases, and — read this part carefully — how to delegate recon work to open models instead of burning frontier tokens on it.

One rule that governs how you work here: Cercano's whole reason to exist is keeping frontier tokens for frontier-grade reasoning and pushing everything else onto local (open) models. Do NOT grind through piles of Grep/Read/Glob yourself to understand how code works — that is exactly the work an open model does fine. Delegate it with the native 'dispatch' tool (it is a first-class agent tool here, not "just an MCP skill"), giving a concrete intent like "trace how model reloading works and return the code path, snippets, and file:line locations" plus a read-only tool grant. The self-dev doc's "Delegate to open models" section explains the two axes (how much brain the task needs; locus decides where that runs) — internalize it.

When you've read them, give a two-or-three-sentence summary of the current state and stop — the user will direct the work from there.`, repo)
}
