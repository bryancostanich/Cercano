package builtins

import (
	"cercano/source/server/internal/capabilities"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRejectsUnsupportedArgumentsBeforeIO(t *testing.T) {
	for _, args := range []string{`{"path":"missing","offset":"640","limit":"140"}`, `{"path":"missing","start":1,"typo":2}`, `{"path":"missing","start":9,"end":2}`, `{"path":"missing","start":-1}`, `{"path":"missing","start":0}`} {
		_, err := ReadFile().Execute(context.Background(), &capabilities.Call{Args: []byte(args)})
		if err == nil || strings.Contains(err.Error(), "no such file") {
			t.Fatalf("must reject before I/O: args=%s err=%v", args, err)
		}
	}
}
func TestReadValidBounds(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), []byte("a\nb\nc"), 0600)
	for _, args := range []string{`{"path":"f"}`, `{"path":"f","start":2}`, `{"path":"f","end":2}`, `{"path":"f","start":2,"end":2}`} {
		if _, err := ReadFile().Execute(context.Background(), &capabilities.Call{Args: []byte(args), WorkDir: dir}); err != nil {
			t.Fatal(err)
		}
	}
}
