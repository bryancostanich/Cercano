package builtins

import (
	"cercano/source/server/internal/capabilities"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteMissingContentCannotEraseExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": path, "replacement": "new"})
	_, err := WriteFile().Execute(t.Context(), &capabilities.Call{Args: args})
	got, _ := os.ReadFile(path)
	if err == nil || string(got) != "original" {
		t.Fatalf("invalid arguments executed: err=%v remaining=%q", err, got)
	}
}

func TestFilesystemAndCommandContractsRejectUnknownBeforeAction(t *testing.T) {
	for _, cap := range []capabilities.Capability{ReadFile(), WriteFile(), EditFile(), ListDir(), StatFile(), Glob(), Grep(), RmFile(), RunCommand()} {
		_, err := cap.Execute(t.Context(), &capabilities.Call{Args: []byte(`{"unsupported":"SECRET-VALUE"}`)})
		if err == nil || !strings.Contains(err.Error(), "unsupported argument") || strings.Contains(err.Error(), "SECRET-VALUE") {
			t.Fatalf("%s: %v", cap.Name(), err)
		}
		var schema map[string]any
		_ = json.Unmarshal(cap.Schema(), &schema)
		if schema["additionalProperties"] != false {
			t.Fatalf("%s must advertise its closed argument contract", cap.Name())
		}
	}
}
func TestWriteExplicitEmptyContentRemainsValid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty")
	raw, _ := json.Marshal(map[string]any{"path": p, "content": ""})
	if _, err := WriteFile().Execute(t.Context(), &capabilities.Call{Args: raw}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(p); err != nil || len(data) != 0 {
		t.Fatal("explicit empty write rejected")
	}
}

func TestDeclaredArgumentSchemasConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var args writeFileArgs
			if err := decodeDeclaredArguments([]byte(`{"path":"f","content":"ok"}`), WriteFile(), &args); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
