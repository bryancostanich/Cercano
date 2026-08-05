//go:build windows

package llamaserver

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// defaultRefreshPATH re-reads the machine and user PATH values a managed
// install (winget) just persisted to the registry and merges them into this
// process's in-memory environment. winget itself warns "Path environment
// variable modified; restart your shell to use the new value." — it writes
// the new PATH to the registry and broadcasts WM_SETTINGCHANGE, but neither
// step updates an already-running process's environment block. Without this,
// the headless Detect the caller runs right after a successful Install would
// still report "llama-server: executable file not found in %PATH%" even
// though the binary now exists on disk.
func defaultRefreshPATH() {
	machine := readRegistryPath(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)
	user := readRegistryPath(registry.CURRENT_USER, `Environment`)
	merged := mergePathValues(os.Getenv("PATH"), machine, user)
	if merged != "" {
		os.Setenv("PATH", merged)
	}
}

// readRegistryPath returns the persisted Path value under the given root/key,
// or "" if the key/value is absent or unreadable — callers treat that as
// "nothing to add" rather than an error worth surfacing.
func readRegistryPath(root registry.Key, path string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	// PATH is stored as REG_EXPAND_SZ (e.g. "%SystemRoot%\system32;...");
	// GetStringValue returns it unexpanded, so expand explicitly.
	val, _, err := k.GetStringValue("Path")
	if err != nil {
		return ""
	}
	expanded, err := registry.ExpandString(val)
	if err != nil {
		return val
	}
	return expanded
}

// mergePathValues concatenates PATH-style strings and de-duplicates entries
// case-insensitively (Windows path lookups already are) while preserving
// first-seen order, so nothing that already resolved stops resolving.
func mergePathValues(parts ...string) string {
	seen := make(map[string]bool)
	var out []string
	for _, part := range parts {
		for _, entry := range strings.Split(part, ";") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			key := strings.ToLower(entry)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, entry)
		}
	}
	return strings.Join(out, ";")
}
