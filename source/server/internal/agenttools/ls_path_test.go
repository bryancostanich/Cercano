package agenttools

import (
	"os"
	"path/filepath"
	"testing"
)

// LS must hand the model the full path of each entry, not just the bare name —
// otherwise the model loses the directory hierarchy and reconstructs wrong
// nested paths (e.g. a/c instead of a/b/c). Mirrors what Glob already returns.
func TestListDir_RowsIncludeFullPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := mustExec(t, ListDir(), map[string]any{"path": dir})

	var got any
	for _, row := range res.Rows {
		if row["name"] == "sub" {
			got = row["path"]
		}
	}
	want := filepath.Join(dir, "sub")
	if got != want {
		t.Errorf("LS entry for 'sub' should carry full path %q, got %v", want, got)
	}
}
