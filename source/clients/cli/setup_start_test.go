package main

import (
	"cercano/source/server/pkg/setupstate"
	"os"
	"path/filepath"
	"testing"
)

func TestFreshSetupWithPreservedConfiguration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	t.Setenv("CERCANO_WIZARD_STATE", filepath.Join(root, "wizard_state.yaml"))
	if !needsSetup(false, path) {
		t.Fatal("missing config should start setup")
	}
	data := []byte("port: 5555\npreferences: {keep: true}\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if needsSetup(false, path) {
		t.Fatal("configured installation unexpectedly starts setup")
	}
	if err := setupstate.Save(setupstate.Fresh()); err != nil {
		t.Fatal(err)
	}
	if !needsSetup(false, path) {
		t.Fatal("fresh resume state ignored when config retained")
	}
	s, ok := setupstate.Load()
	if !ok || s.Step != setupstate.StepLocus || s.Baseline != nil {
		t.Fatal("old answers or baseline restored")
	}
	if err := setupstate.Clear(); err != nil {
		t.Fatal(err)
	}
	if needsSetup(false, path) {
		t.Fatal("completed setup reopened")
	}
	if !needsSetup(true, path) {
		t.Fatal("explicit setup ignored")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(data) {
		t.Fatal("startup changed preserved config")
	}
}
