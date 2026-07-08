package watchdog

import "sync"

// Tool-name canonicalization. The standalone agent registers several
// capabilities under display aliases (Edit for edit_file, Bash for
// run_command, Write for write_file, …), and both the gated action and the
// transcript blocks carry those display names. Checks match on canonical
// capability names, so every tool-name read funnels through canonical().
// Without this the alias split silently killed most checks: worktree-first
// waited for "run_command" while the registry only ever emitted "Bash".
var (
	aliasMu          sync.RWMutex
	aliasToCanonical = map[string]string{}
)

// SetDisplayAliases records the canonical→display alias table (as exported by
// the capability registry) for reverse lookup. Called at watchdog build time;
// safe to call repeatedly.
func SetDisplayAliases(canonicalToDisplay map[string]string) {
	aliasMu.Lock()
	defer aliasMu.Unlock()
	for c, d := range canonicalToDisplay {
		if d != "" && d != c {
			aliasToCanonical[d] = c
		}
	}
}

// canonical maps a display tool name to its canonical capability name.
// Unknown names pass through unchanged.
func canonical(name string) string {
	aliasMu.RLock()
	defer aliasMu.RUnlock()
	if c, ok := aliasToCanonical[name]; ok {
		return c
	}
	return name
}
