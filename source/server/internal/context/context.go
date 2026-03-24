package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// CercanoDir is the directory name inside a project root.
	CercanoDir = ".cercano"
	// ContextFile is the context file name inside CercanoDir.
	ContextFile = "context.md"
)

// ContextPath returns the full path to the context file for a project.
func ContextPath(projectDir string) string {
	return filepath.Join(projectDir, CercanoDir, ContextFile)
}

// Loader reads and caches project context from .cercano/context.md.
type Loader struct {
	mu      sync.RWMutex
	cache   map[string]string // projectDir -> context content
	nudged  map[string]bool   // projectDir -> already nudged this session
}

// NewLoader creates a new context Loader.
func NewLoader() *Loader {
	return &Loader{
		cache:  make(map[string]string),
		nudged: make(map[string]bool),
	}
}

// Load reads the context file for a project. Returns empty string if not found.
// Results are cached per project directory.
func (l *Loader) Load(projectDir string) (string, error) {
	if projectDir == "" {
		return "", nil
	}

	l.mu.RLock()
	if cached, ok := l.cache[projectDir]; ok {
		l.mu.RUnlock()
		return cached, nil
	}
	l.mu.RUnlock()

	path := ContextPath(projectDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			l.mu.Lock()
			l.cache[projectDir] = ""
			l.mu.Unlock()
			return "", nil
		}
		return "", fmt.Errorf("failed to read context file %q: %w", path, err)
	}

	content := string(data)
	l.mu.Lock()
	l.cache[projectDir] = content
	l.mu.Unlock()
	return content, nil
}

// IsInitialized returns true if .cercano/context.md exists for the project.
func (l *Loader) IsInitialized(projectDir string) bool {
	if projectDir == "" {
		return false
	}
	_, err := os.Stat(ContextPath(projectDir))
	return err == nil
}

// Invalidate clears the cached context for a project, forcing a re-read on next Load.
func (l *Loader) Invalidate(projectDir string) {
	l.mu.Lock()
	delete(l.cache, projectDir)
	l.mu.Unlock()
}

// PrependContext loads the project context and prepends it to the prompt.
// Returns the original prompt unchanged if no context is available.
func (l *Loader) PrependContext(projectDir, prompt string) string {
	if projectDir == "" {
		return prompt
	}
	ctx, _ := l.Load(projectDir)
	if ctx == "" {
		return prompt
	}
	return fmt.Sprintf("Project Context:\n%s\n\n---\n\n%s", ctx, prompt)
}

// NudgeNeeded returns true if the project has no context.md and hasn't been
// nudged yet this session. Returns false for empty projectDir or initialized projects.
func (l *Loader) NudgeNeeded(projectDir string) bool {
	if projectDir == "" {
		return false
	}
	if l.IsInitialized(projectDir) {
		return false
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nudged[projectDir] {
		return false
	}
	l.nudged[projectDir] = true
	return true
}
