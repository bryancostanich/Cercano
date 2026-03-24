package context

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxFileSize is the maximum file size to read (64KB).
	MaxFileSize = 64 * 1024
	// MaxFiles is the maximum number of files to include.
	MaxFiles = 50
)

// DiscoveredFile represents a file found during project scanning.
type DiscoveredFile struct {
	Path     string // absolute path
	RelPath  string // relative to project root
	Content  string // file content
	Priority int    // lower = higher priority
}

// Scanner discovers and reads key project files.
type Scanner struct{}

// NewScanner creates a new Scanner.
func NewScanner() *Scanner {
	return &Scanner{}
}

// skipDirs are directories to always skip during scanning.
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"out":          true,
	"__pycache__":  true,
	".venv":        true,
	".tox":         true,
}

// highPriorityNames are filenames that get scanned first (priority 0).
var highPriorityNames = map[string]bool{
	"CLAUDE.md":      true,
	"README.md":      true,
	"README":         true,
	"readme.md":      true,
}

// midPriorityNames are config/manifest files (priority 1).
var midPriorityNames = map[string]bool{
	"go.mod":         true,
	"go.sum":         false, // skip, not useful
	"package.json":   true,
	"Cargo.toml":     true,
	"pyproject.toml": true,
	"Makefile":       true,
	"CMakeLists.txt": true,
	"docker-compose.yml": true,
	"Dockerfile":     true,
}

// highPriorityExtensions are file extensions that get priority 2.
var highPriorityExtensions = map[string]bool{
	".proto": true,
	".h":     true,
	".hpp":   true,
	".thrift": true,
	".graphql": true,
	".schema": true,
}

// DiscoverFiles walks the project directory and returns key files sorted by priority.
func (s *Scanner) DiscoverFiles(projectDir string) ([]DiscoveredFile, error) {
	var files []DiscoveredFile

	err := filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}

		// Skip ignored directories
		if info.IsDir() {
			name := info.Name()
			if skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files over size limit
		if info.Size() > MaxFileSize {
			return nil
		}

		// Skip binary-looking files
		if isBinaryExtension(filepath.Ext(path)) {
			return nil
		}

		relPath, _ := filepath.Rel(projectDir, path)
		priority := filePriority(relPath, info.Name())

		// Only include files we care about
		if priority < 0 {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		files = append(files, DiscoveredFile{
			Path:     path,
			RelPath:  relPath,
			Content:  string(content),
			Priority: priority,
		})

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort by priority, then by path
	sort.Slice(files, func(i, j int) bool {
		if files[i].Priority != files[j].Priority {
			return files[i].Priority < files[j].Priority
		}
		return files[i].RelPath < files[j].RelPath
	})

	// Cap total files
	if len(files) > MaxFiles {
		files = files[:MaxFiles]
	}

	return files, nil
}

// filePriority returns the priority for a file. Returns -1 if the file should be skipped.
func filePriority(relPath, name string) int {
	// Claude memory files
	if strings.HasPrefix(relPath, filepath.Join(".claude", "memory")) {
		return 0
	}

	// High-priority by name
	if highPriorityNames[name] {
		return 0
	}

	// Mid-priority config files
	if midPriorityNames[name] {
		return 1
	}

	// High-priority extensions
	ext := filepath.Ext(name)
	if highPriorityExtensions[ext] {
		return 2
	}

	// Config files by extension
	switch ext {
	case ".yaml", ".yml", ".toml", ".json", ".ini", ".cfg":
		// Only include config-looking files at project root or config dirs
		dir := filepath.Dir(relPath)
		if dir == "." || dir == "config" || dir == "configs" || dir == ".github" {
			return 3
		}
	}

	// Skip everything else
	return -1
}

// isBinaryExtension returns true for file extensions that are likely binary.
func isBinaryExtension(ext string) bool {
	switch ext {
	case ".exe", ".bin", ".so", ".dylib", ".dll", ".o", ".a",
		".png", ".jpg", ".jpeg", ".gif", ".ico", ".svg",
		".zip", ".tar", ".gz", ".bz2", ".xz",
		".wasm", ".pyc", ".class",
		".db", ".sqlite", ".sqlite3":
		return true
	}
	return false
}
