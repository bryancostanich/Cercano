package ui

import (
	"os"
	"strings"
	"testing"
)

func TestWriteExport_WritesFile(t *testing.T) {
	dir := t.TempDir()
	path, err := writeExport(dir, "abcdef0123456789", `[{"role":"user"}]`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("export not written: %v", err)
	}
	if !strings.Contains(string(b), `"role":"user"`) {
		t.Errorf("export content wrong: %s", b)
	}
	if !strings.Contains(path, "abcdef01") {
		t.Errorf("filename should include the conv id prefix: %s", path)
	}
}
