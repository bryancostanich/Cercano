package tools_test

import (
	"context"
	"testing"

	"cercano/source/server/internal/testfixtures"
	"cercano/source/server/internal/tools"
)

func TestGoValidator_Validate(t *testing.T) {
	v := tools.NewGoValidator()
	ctx := context.Background()

	t.Run("ValidNoTests", func(t *testing.T) {
		dir := testfixtures.Open(t, "go/valid")
		decision, err := v.Validate(ctx, dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if decision != tools.Passed {
			t.Errorf("got decision %s, want passed", decision)
		}
	})

	t.Run("CompilationFailure", func(t *testing.T) {
		dir := testfixtures.Open(t, "go/broken")
		decision, err := v.Validate(ctx, dir)
		if err == nil {
			t.Fatal("expected error for compilation failure, got nil")
		}
		if decision != tools.Failed {
			t.Errorf("got decision %s, want failed", decision)
		}
	})

	t.Run("ValidWithTests", func(t *testing.T) {
		dir := testfixtures.Open(t, "go/needs-tests")
		decision, err := v.Validate(ctx, dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if decision != tools.Passed {
			t.Errorf("got decision %s, want passed", decision)
		}
	})
}
