// Package slash implements the cercano-cli slash-command registry.
// Algorithmic prefix-match dispatch — no LLM involved in command selection.
package slash

import (
	"sort"
	"strings"
)

// Command is one entry in the registry.
type Command struct {
	Name    string   // canonical name, no leading slash
	Aliases []string // optional alternates
	Help    string   // one-line description for /help
	// Handler is called with the parsed args (whitespace-split, after the command name).
	// It returns a Result describing what the UI should do.
	Handler func(args []string) Result
}

// ResultKind tags what kind of side effect the UI should perform.
type ResultKind int

const (
	ResultText ResultKind = iota
	ResultQuit
	ResultClearConversation
	ResultOpenSettings
	ResultOpenHistoryPicker
	ResultOpenRuntimeDashboard
	ResultOpenContextView
	ResultResumeConversation // Text field carries the conversation id
	ResultSetPromptColor     // Text field carries the parsed hex (#RRGGBB) or a palette key
	ResultSetSessionTitle    // Text field carries the new title
	ResultInvokeTool         // ToolName + ToolArgs carry the tool to invoke (CLI may prompt-confirm first)
	ResultSetPermissionMode  // PermissionMode carries the new mode (strict|permissive|bypass)
	ResultDevMode            // WorkDir carries the resolved Cercano repo root
)

// Result is what a slash command produces.
type Result struct {
	Kind           ResultKind
	Text           string // for ResultText / Set* result kinds
	ToolName       string // for ResultInvokeTool
	ToolArgs       string // for ResultInvokeTool (JSON-encoded args)
	PermissionMode string // for ResultSetPermissionMode
	WorkDir        string // for ResultDevMode
}

// Registry holds the set of registered commands and supports prefix dispatch.
type Registry struct {
	cmds map[string]*Command
}

// New returns an empty registry.
func New() *Registry { return &Registry{cmds: map[string]*Command{}} }

// Register adds a command. Panics on duplicate names so the bug surfaces at startup.
func (r *Registry) Register(c Command) {
	if _, exists := r.cmds[c.Name]; exists {
		panic("slash: duplicate command name: " + c.Name)
	}
	r.cmds[c.Name] = &c
	for _, a := range c.Aliases {
		if _, exists := r.cmds[a]; exists {
			panic("slash: duplicate alias: " + a)
		}
		r.cmds[a] = &c
	}
}

// All returns commands sorted by canonical name, deduplicated.
func (r *Registry) All() []*Command {
	seen := map[*Command]bool{}
	var out []*Command
	for _, c := range r.cmds {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns the command registered under `name` (including alias
// lookups). The returned bool reports presence; the command pointer is
// nil when not found.
func (r *Registry) Lookup(name string) (*Command, bool) {
	c, ok := r.cmds[name]
	return c, ok
}

// PrefixMatches returns the canonical command names whose name starts with
// `prefix` (leading `/` is stripped). Sorted, no aliases, deduped. Used by
// the CLI to render autocomplete suggestions.
func (r *Registry) PrefixMatches(prefix string) []string {
	prefix = strings.TrimPrefix(prefix, "/")
	seen := map[*Command]bool{}
	var names []string
	for n, c := range r.cmds {
		if c.Name != n {
			continue // skip aliases
		}
		if strings.HasPrefix(n, prefix) && !seen[c] {
			seen[c] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// CommonPrefix returns the longest leading substring shared by all names.
// Used for Tab-complete-to-longest-prefix behavior. Empty if names is empty.
func CommonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	p := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// Dispatch parses an input line (with or without leading slash) and runs the
// matching command. Returns ok=false with a Result containing a suggestion if
// no exact match is found.
func (r *Registry) Dispatch(line string) (Result, bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return Result{Kind: ResultText, Text: "(no command)"}, false
	}
	parts := strings.Fields(line)
	name := parts[0]
	args := parts[1:]

	if c, ok := r.cmds[name]; ok {
		return c.Handler(args), true
	}
	// Fuzzy suggestion: shortest command name whose canonical form starts with the input.
	var hint string
	bestLen := 1 << 30
	for n, c := range r.cmds {
		if c.Name != n {
			continue // skip aliases
		}
		if strings.HasPrefix(n, name) && len(n) < bestLen {
			hint = n
			bestLen = len(n)
		}
	}
	if hint != "" {
		return Result{Kind: ResultText, Text: "unknown command /" + name + " — did you mean /" + hint + "?"}, false
	}
	return Result{Kind: ResultText, Text: "unknown command /" + name + " (try /help)"}, false
}
