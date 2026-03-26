package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupFile_CreatesBackup(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	content := "package main\n\nfunc main() {}\n"
	os.WriteFile(filePath, []byte(content), 0644)

	backupPath, err := BackupFile(filePath)
	if err != nil {
		t.Fatalf("BackupFile: %v", err)
	}

	// Verify backup exists and has correct content
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != content {
		t.Errorf("backup content mismatch: got %q, want %q", string(data), content)
	}

	// Verify backup is in .cercano/backups/
	if !strings.Contains(backupPath, ".cercano/backups/") {
		t.Errorf("backup path should be in .cercano/backups/, got %s", backupPath)
	}
}

func TestRestoreFile_RestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	original := "package main\n\nfunc main() {}\n"
	modified := "package main\n\n// Modified.\nfunc main() {}\n"

	os.WriteFile(filePath, []byte(original), 0644)
	backupPath, _ := BackupFile(filePath)

	// Modify the original
	os.WriteFile(filePath, []byte(modified), 0644)

	// Restore
	if err := RestoreFile(filePath, backupPath); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != original {
		t.Errorf("restored content mismatch: got %q, want %q", string(data), original)
	}
}

func TestApplyEdits_SingleFunction(t *testing.T) {
	source := `package main

func Hello() string {
	return "hello"
}
`
	edits := []DocEdit{
		{Line: 3, Comment: "// Hello returns a greeting."},
	}

	result, err := ApplyEdits(source, edits)
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}

	if !strings.Contains(result, "// Hello returns a greeting.\nfunc Hello()") {
		t.Errorf("expected comment before function, got:\n%s", result)
	}
}

func TestApplyEdits_MultipleSymbols(t *testing.T) {
	source := `package main

func Hello() string {
	return "hello"
}

func World() string {
	return "world"
}
`
	edits := []DocEdit{
		{Line: 3, Comment: "// Hello returns a greeting."},
		{Line: 7, Comment: "// World returns the world."},
	}

	result, err := ApplyEdits(source, edits)
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}

	if !strings.Contains(result, "// Hello returns a greeting.\nfunc Hello()") {
		t.Errorf("expected comment before Hello, got:\n%s", result)
	}
	if !strings.Contains(result, "// World returns the world.\nfunc World()") {
		t.Errorf("expected comment before World, got:\n%s", result)
	}
}

func TestApplyEdits_PreservesExisting(t *testing.T) {
	source := `package main

// Existing doc comment.
func Documented() {}

func Undocumented() {}
`
	edits := []DocEdit{
		{Line: 6, Comment: "// Undocumented does something."},
	}

	result, err := ApplyEdits(source, edits)
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}

	// Existing comment should be untouched
	if !strings.Contains(result, "// Existing doc comment.\nfunc Documented()") {
		t.Errorf("existing doc should be preserved, got:\n%s", result)
	}
	if !strings.Contains(result, "// Undocumented does something.\nfunc Undocumented()") {
		t.Errorf("new doc should be inserted, got:\n%s", result)
	}
}

func TestInsertDocComments_FormatsResult(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	source := "package main\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"
	os.WriteFile(filePath, []byte(source), 0644)

	edits := []DocEdit{
		{Line: 3, Comment: "// Hello returns a greeting."},
	}

	if err := InsertDocComments(filePath, edits); err != nil {
		t.Fatalf("InsertDocComments: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	result := string(data)
	if !strings.Contains(result, "// Hello returns a greeting.") {
		t.Errorf("expected doc comment in result, got:\n%s", result)
	}
}

func TestApplyEdits_OutOfRange(t *testing.T) {
	source := "package main\n"
	edits := []DocEdit{
		{Line: 100, Comment: "// Out of range."},
	}

	_, err := ApplyEdits(source, edits)
	if err == nil {
		t.Error("expected error for out-of-range line")
	}
}
