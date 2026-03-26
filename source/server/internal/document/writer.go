package document

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DocEdit represents a doc comment to insert at a specific line.
type DocEdit struct {
	Line    int    // 1-based line number to insert BEFORE
	Comment string // formatted Go doc comment (// prefixed lines)
}

// BackupFile copies the file to .cercano/backups/<basename>.<timestamp>.
// Returns the backup path.
func BackupFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read original: %w", err)
	}

	dir := filepath.Join(filepath.Dir(filePath), ".cercano", "backups")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	base := filepath.Base(filePath)
	backupName := fmt.Sprintf("%s.%d", base, time.Now().Unix())
	backupPath := filepath.Join(dir, backupName)

	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return "", fmt.Errorf("write backup: %w", err)
	}
	return backupPath, nil
}

// RestoreFile copies the backup back to the original path.
func RestoreFile(filePath, backupPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("write restored: %w", err)
	}
	return nil
}

// InsertDocComments inserts doc comments into the source file at the specified
// line positions. Edits are applied from bottom to top to preserve line numbers.
// The result is formatted with go/format to ensure valid Go formatting.
func InsertDocComments(filePath string, edits []DocEdit) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	result, err := ApplyEdits(string(data), edits)
	if err != nil {
		return err
	}

	// Format the result to ensure valid Go
	formatted, err := format.Source([]byte(result))
	if err != nil {
		return fmt.Errorf("go format failed after edits: %w", err)
	}

	if err := os.WriteFile(filePath, formatted, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// ApplyEdits inserts doc comment edits into source text. Exported for testing.
func ApplyEdits(source string, edits []DocEdit) (string, error) {
	if len(edits) == 0 {
		return source, nil
	}

	lines := strings.Split(source, "\n")

	// Sort edits by line number descending so insertions don't shift subsequent positions
	sorted := make([]DocEdit, len(edits))
	copy(sorted, edits)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Line > sorted[j].Line
	})

	for _, edit := range sorted {
		if edit.Line < 1 || edit.Line > len(lines)+1 {
			return "", fmt.Errorf("edit line %d out of range (file has %d lines)", edit.Line, len(lines))
		}

		insertIdx := edit.Line - 1 // convert to 0-based

		// Build insertion: comment lines
		commentLines := strings.Split(edit.Comment, "\n")

		// Insert comment lines before the target line
		newLines := make([]string, 0, len(lines)+len(commentLines))
		newLines = append(newLines, lines[:insertIdx]...)
		newLines = append(newLines, commentLines...)
		newLines = append(newLines, lines[insertIdx:]...)
		lines = newLines
	}

	return strings.Join(lines, "\n"), nil
}
