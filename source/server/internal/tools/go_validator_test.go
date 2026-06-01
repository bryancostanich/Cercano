package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/tools"
)

func TestGoValidator_Validate(t *testing.T) {
	v := tools.NewGoValidator()
	ctx := context.Background()

	t.Run("ValidCode", func(t *testing.T) {
		tmpDir := t.TempDir()
		err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module temp\n"), 0644)
		if err != nil {
			t.Fatal(err)
		}
		code := `package temp
import "testing"
func TestPass(t *testing.T) {}
`
		err = os.WriteFile(filepath.Join(tmpDir, "pass_test.go"), []byte(code), 0644)
		if err != nil {
			t.Fatal(err)
		}

		decision, err := v.Validate(ctx, tmpDir)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if decision != tools.Passed {
			t.Errorf("got decision %s, want passed", decision)
		}
	})

	t.Run("CompilationFailure", func(t *testing.T) {
		tmpDir := t.TempDir()
		err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module temp\n"), 0644)
		if err != nil {
			t.Fatal(err)
		}
		code := `package temp
import "testing"
func TestFail(t *testing.T) {
	undefined_variable
}
`
		err = os.WriteFile(filepath.Join(tmpDir, "fail_test.go"), []byte(code), 0644)
		if err != nil {
			t.Fatal(err)
		}

		decision, err := v.Validate(ctx, tmpDir)
		if err == nil {
			t.Fatal("expected error for compilation failure, got nil")
		}
		if decision != tools.Failed {
			t.Errorf("got decision %s, want failed", decision)
		}
	})

	t.Run("TestFailure", func(t *testing.T) {
		tmpDir := t.TempDir()
		err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module temp\n"), 0644)
		if err != nil {
			t.Fatal(err)
		}
		code := `package temp
import "testing"
func TestFail(t *testing.T) {
	t.Fatal("intended failure")
}
`
		err = os.WriteFile(filepath.Join(tmpDir, "fail_test.go"), []byte(code), 0644)
		if err != nil {
			t.Fatal(err)
		}

		decision, err := v.Validate(ctx, tmpDir)
		if err == nil {
			t.Fatal("expected error for test failure, got nil")
		}
		if decision != tools.Failed {
			t.Errorf("got decision %s, want failed", decision)
		}
	})
}
