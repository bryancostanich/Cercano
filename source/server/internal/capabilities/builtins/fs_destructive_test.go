package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

// -- rm_file tests --

func TestRmFileCapability_Metadata(t *testing.T) {
	cap := RmFile()
	if cap.Name() != "rm_file" {
		t.Fatalf("name = %q, want rm_file", cap.Name())
	}
	if cap.Tier() != capabilities.TierX {
		t.Fatalf("tier = %q, want X", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces = %v, want Agent|MCP", cap.Surfaces())
	}
}

func TestRmFileCapability_DeletesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(p, []byte("goodbye"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	args, _ := json.Marshal(map[string]any{"path": p})
	res, err := RmFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Type != capabilities.ResultText {
		t.Fatalf("type = %q, want text", res.Type)
	}
	if !strings.Contains(res.Text, "deleted") {
		t.Fatalf("result text = %q, want to contain 'deleted'", res.Text)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file still exists after rm_file")
	}
}

func TestRmFileCapability_RefusesDirectory(t *testing.T) {
	dir := t.TempDir()

	args, _ := json.Marshal(map[string]any{"path": dir})
	_, err := RmFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error when path is a directory")
	}
	if !strings.Contains(err.Error(), "rm_file") {
		t.Fatalf("error should be prefixed rm_file, got: %v", err)
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error should mention directory, got: %v", err)
	}
}

func TestRmFileCapability_MissingPath(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"path": ""})
	_, err := RmFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil || !strings.Contains(err.Error(), "rm_file") {
		t.Fatalf("expected rm_file error for empty path, got %v", err)
	}
}

func TestRmFileCapability_NonExistentFile(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"path": "/tmp/cercano_test_nonexistent_12345.txt"})
	_, err := RmFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil || !strings.Contains(err.Error(), "rm_file") {
		t.Fatalf("expected rm_file error for non-existent file, got %v", err)
	}
}
