package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestGrepCapability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc hello() string {\n\treturn \"hello world\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cap := Grep()
	if cap.Name() != "grep" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierR {
		t.Fatalf("tier wrong: %q", cap.Tier())
	}
	if cap.Surfaces() != capabilities.SurfaceAgent|capabilities.SurfaceMCP {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}

	// Real match: search for "hello" in the temp dir.
	args, _ := json.Marshal(map[string]any{"pattern": "hello", "path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != capabilities.ResultRows {
		t.Fatalf("expected rows result, got %q", res.Type)
	}
	if len(res.Rows) == 0 {
		t.Fatal("expected at least one match row")
	}
	// Each row must have path and content.
	for _, row := range res.Rows {
		if _, ok := row["path"]; !ok {
			t.Fatalf("row missing path: %v", row)
		}
		if _, ok := row["content"]; !ok {
			t.Fatalf("row missing content: %v", row)
		}
	}
}

func TestGrepCapability_NoPattern(t *testing.T) {
	cap := Grep()
	args, _ := json.Marshal(map[string]any{})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestGrepCapability_ZeroMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cap := Grep()
	args, _ := json.Marshal(map[string]any{"pattern": "ZZZXXX_NOMATCH", "path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != capabilities.ResultRows {
		t.Fatalf("expected rows result, got %q", res.Type)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected zero rows, got %d", len(res.Rows))
	}
	if res.Detail != "0 matches" {
		t.Fatalf("expected detail '0 matches', got %q", res.Detail)
	}
}

func TestGrep_SearchesWorkDirByDefault(t *testing.T) {
	// Create a temp dir with content.
	contentDir := t.TempDir()

	// Write a unique pattern file only in contentDir.
	needle := "UNIQUE_NEEDLE_FOR_TEST_12345"
	if err := os.WriteFile(filepath.Join(contentDir, "found.txt"), []byte(needle), 0o644); err != nil {
		t.Fatal(err)
	}

	cap := Grep()
	// Execute grep with no explicit path (defaults to "."), but with WorkDir set to contentDir.
	args, _ := json.Marshal(map[string]any{"pattern": needle})
	res, err := cap.Execute(context.Background(), &capabilities.Call{WorkDir: contentDir, Args: args, Emit: func(string) {}})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	if res.Type != capabilities.ResultRows {
		t.Fatalf("expected rows result, got %q", res.Type)
	}
	if len(res.Rows) == 0 {
		t.Errorf("grep missed the file under WorkDir: got 0 rows, expected >=1")
	}

	// Verify one of the rows points to the file we created.
	foundFile := false
	for _, row := range res.Rows {
		if p, ok := row["path"].(string); ok && (p == "found.txt" || filepath.Base(p) == "found.txt") {
			foundFile = true
			break
		}
	}
	if !foundFile {
		t.Errorf("grep didn't find the expected file path; rows: %v", res.Rows)
	}
}
