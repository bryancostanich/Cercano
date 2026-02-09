package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/internal/agent"
)

func TestGoTestValidator_Validate(t *testing.T) {
	v := agent.NewGoTestValidator()
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

		err = v.Validate(ctx, tmpDir)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
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

		err = v.Validate(ctx, tmpDir)
		if err == nil {
			t.Error("expected error for compilation failure, got nil")
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

		err = v.Validate(ctx, tmpDir)
		if err == nil {
			t.Error("expected error for test failure, got nil")
		}
	})
}
