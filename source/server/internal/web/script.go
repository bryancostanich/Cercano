package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	_ "embed"
)

// The DuckDuckGo search script is embedded in the binary and materialized to
// disk on demand. The previous scheme resolved <bin dir>/../scripts/ relative
// to the running executable, which only holds for repo-layout builds — every
// installed binary (dev launcher, future Homebrew) silently lost web search.
//
//go:embed ddg_search.py
var ddgScriptSource []byte

// DefaultScriptDir is where embedded runtime scripts are materialized:
// ~/.config/cercano/scripts. Empty when the home directory is unresolvable.
func DefaultScriptDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "cercano", "scripts")
}

// EnsureSearchScript writes the embedded search script into dir (defaulting
// to DefaultScriptDir) and returns its path. The write only happens when the
// on-disk copy is missing or differs from this binary's embedded copy, so
// repeat calls are cheap and stale copies from older builds refresh
// themselves. The write is atomic (temp file + rename) so a concurrent
// reader never sees a partial script.
func EnsureSearchScript(dir string) (string, error) {
	if dir == "" {
		dir = DefaultScriptDir()
	}
	if dir == "" {
		return "", fmt.Errorf("resolve script dir: no home directory")
	}
	path := filepath.Join(dir, "ddg_search.py")
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, ddgScriptSource) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create script dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ddg_search-*.py")
	if err != nil {
		return "", fmt.Errorf("stage search script: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(ddgScriptSource); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("write search script: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("write search script: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("install search script: %w", err)
	}
	return path, nil
}
