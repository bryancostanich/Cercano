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
