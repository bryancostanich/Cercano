package agent

import (
	"encoding/json"
	"strings"
)

// requiresForcedConfirm reports whether a tool call must ALWAYS get a human
// confirmation prompt, regardless of permission mode — even Bypass.
//
// The motivating case is a WIP-consuming `git stash` run through the shell
// (run_command / its "Bash" display alias). Such a command silently hides the
// working tree's uncommitted changes; an agent that runs one on its own
// initiative — as a "tidy the tree first" reflex before a land, say — can
// bury a human's in-progress work with no prompt under Permissive or Bypass.
// Forcing a confirm here means the human is always the one who authorizes
// setting their own WIP aside; the agent has no self-service path to it.
//
// This is intentionally narrow: only stash verbs that *consume* WIP
// (push/save/create, and bare `git stash` which defaults to push) qualify.
// The restoring/inspecting verbs (pop/apply/list/show/drop) never do — they
// bring WIP back or just read it, and blocking those would be counterproductive.
func requiresForcedConfirm(toolName string, args json.RawMessage) bool {
	if !isShellToolName(toolName) {
		return false
	}
	var in struct {
		Cmd []string `json:"cmd"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return false
	}
	return isWIPConsumingStash(in.Cmd)
}

// isShellToolName reports whether name is the shell-execution capability under
// either its canonical registry name or its standalone display alias. Both can
// appear on the wire depending on which surface the model was trained against,
// so the guard must recognize both — matching only one is a silent bypass.
func isShellToolName(name string) bool {
	return name == "run_command" || name == "Bash"
}

// isWIPConsumingStash reports whether argv runs a `git stash` that sets
// uncommitted work aside (push/save/create/store, or bare `git stash`).
//
// It splits argv into the individual simple commands that would actually run
// (respecting shell separators and unwrapping `sh -c "…"` script arguments),
// then checks each one for a leading `git … stash <consuming-verb>`. This is
// deliberately stricter than searching a flattened token soup: `echo git stash`
// and `git status; foo stash` must NOT match, because the actual command verb
// is `echo`/`foo`, not `git`.
func isWIPConsumingStash(argv []string) bool {
	for _, cmd := range simpleCommands(argv) {
		if commandIsConsumingStash(cmd) {
			return true
		}
	}
	return false
}

// commandIsConsumingStash checks a single simple command (already split into
// tokens, no shell separators) for `git stash <consuming-verb>`.
func commandIsConsumingStash(toks []string) bool {
	// The command verb must be git itself — not merely contain "git" somewhere.
	if len(toks) < 2 || toks[0] != "git" {
		return false
	}
	// The next non-flag token after `git` must be `stash` (git global flags
	// like `-C <dir>` may precede the subcommand).
	i := 1
	for i < len(toks) && strings.HasPrefix(toks[i], "-") {
		// `-C dir`, `-c key=val` take an argument; skip it too. Conservative:
		// skipping one extra token can only make us miss a match, never
		// over-match, and the common stash forms carry no such flags.
		i++
	}
	if i >= len(toks) || toks[i] != "stash" {
		return false
	}
	// The verb is the next non-flag token after `stash`, if any.
	for j := i + 1; j < len(toks); j++ {
		if strings.HasPrefix(toks[j], "-") {
			continue // -u / --include-untracked belong to a bare push
		}
		switch toks[j] {
		case "push", "save", "create", "store":
			return true
		default:
			// pop, apply, list, show, drop, clear, branch, … — not consuming.
			return false
		}
	}
	// Bare `git stash` (no verb) defaults to push → consuming.
	return true
}

// simpleCommands splits argv into the list of simple commands that would run,
// as lowercased token slices. It unwraps a shell wrapper's `-c`/`-lc` script
// argument into its own command line, and splits on shell separators
// (&& || ; | &) so a benign command chained before/after a stash-shaped token
// is evaluated independently.
func simpleCommands(argv []string) [][]string {
	if len(argv) == 0 {
		return nil
	}
	head := strings.ToLower(argv[0])
	// Shell wrapper: pull the script string out of `-c`/`-lc`/`-lc` and tokenize it.
	if isShellName(head) {
		if script, ok := shellScriptArg(argv); ok {
			return splitOnSeparators(strings.Fields(strings.ToLower(script)))
		}
		// A shell invoked without an inline script (e.g. `bash script.sh`) —
		// we can't see what it runs, so there is nothing to match here.
		return nil
	}
	// Direct command: split the argv itself on separators (rare, but a caller
	// could pass `["git","status",";","git","stash"]`).
	lower := make([]string, len(argv))
	for i, a := range argv {
		lower[i] = strings.ToLower(a)
	}
	return splitOnSeparators(lower)
}

// shellScriptArg returns the inline script string from a shell argv, i.e. the
// token following a `-c`/`-lc`/`-ic` style flag whose letters include 'c'.
func shellScriptArg(argv []string) (string, bool) {
	for i := 1; i < len(argv)-1; i++ {
		f := argv[i]
		if strings.HasPrefix(f, "-") && strings.Contains(f, "c") {
			return argv[i+1], true
		}
	}
	return "", false
}

// splitOnSeparators breaks a token stream into simple commands at shell
// separators. Separators glued to a token (`status;`) are handled by trimming:
// a token ending in `;`/`&` still terminates the current command.
func splitOnSeparators(toks []string) [][]string {
	var cmds [][]string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			cmds = append(cmds, cur)
			cur = nil
		}
	}
	for _, t := range toks {
		if isShellSeparator(t) {
			flush()
			continue
		}
		// Trim a trailing separator glued to the token (`status;`, `foo&`).
		trimmed := strings.TrimRight(t, ";&|")
		if trimmed != t {
			if trimmed != "" {
				cur = append(cur, trimmed)
			}
			flush()
			continue
		}
		cur = append(cur, t)
	}
	flush()
	return cmds
}

func isShellName(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh":
		return true
	}
	return false
}

func isShellSeparator(tok string) bool {
	switch tok {
	case "&&", "||", ";", "|", "&":
		return true
	}
	return false
}
