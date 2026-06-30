package gitflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveReadsCercanoConfigAndOverride(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	dir := filepath.Join(r.Dir, ".cercano")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "trunk: develop\ntest_command: go test ./...\nreview_floor: 3\nsensitive_paths:\n  - \"source/server/internal/server/**\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gitflow.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Resolve(ctx, r, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Trunk != "develop" || cfg.TestCommand != "go test ./..." || cfg.ReviewFloor != 3 {
		t.Fatalf("config not read: %+v", cfg)
	}

	// Override wins over file.
	cfg2, err := Resolve(ctx, r, Config{Trunk: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Trunk != "main" {
		t.Fatalf("override should win, got %q", cfg2.Trunk)
	}
}

func TestResolveDefaultsReviewFloor(t *testing.T) {
	r := newTestRepo(t)
	// No .cercano config, but pass trunk override so Trunk resolves.
	cfg, err := Resolve(context.Background(), r, Config{Trunk: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReviewFloor != 5 {
		t.Fatalf("review_floor default should be 5, got %d", cfg.ReviewFloor)
	}
}

func TestResolveErrorsWhenTrunkUnresolved(t *testing.T) {
	r := newTestRepo(t) // no origin remote, no config, no override
	if _, err := Resolve(context.Background(), r, Config{}); err == nil {
		t.Fatal("expected error when trunk cannot be resolved")
	}
}
