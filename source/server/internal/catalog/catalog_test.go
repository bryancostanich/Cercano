package catalog

import (
	"context"
	"testing"
)

// fakeBackend is a minimal Backend for exercising the registry.
type fakeBackend struct{ name string }

func (f fakeBackend) Name() string { return f.name }
func (f fakeBackend) List(context.Context, ListOptions) ([]Model, error) {
	return []Model{{Backend: f.name, ID: f.name + "/model"}}, nil
}
func (f fakeBackend) Detail(context.Context, string) (Detail, error) {
	return Detail{Backend: f.name}, nil
}
func (f fakeBackend) ResolveDownload(context.Context, string, string) (DownloadPlan, error) {
	return DownloadPlan{}, nil
}

func TestRegistry_FirstRegisteredIsActive(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Active(); ok {
		t.Fatal("empty registry should have no active backend")
	}
	r.Register(fakeBackend{name: "huggingface"})
	r.Register(fakeBackend{name: "ollama"})
	if r.ActiveName() != "huggingface" {
		t.Errorf("active = %q, want huggingface (first registered)", r.ActiveName())
	}
}

func TestRegistry_SetActive(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeBackend{name: "huggingface"})
	r.Register(fakeBackend{name: "ollama"})

	if err := r.SetActive("ollama"); err != nil {
		t.Fatalf("SetActive(ollama): %v", err)
	}
	b, ok := r.Active()
	if !ok || b.Name() != "ollama" {
		t.Errorf("active backend = %v/%v, want ollama", b, ok)
	}
}

func TestRegistry_SetActiveUnknownFailsLoud(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeBackend{name: "huggingface"})
	err := r.SetActive("bogus")
	if err == nil {
		t.Fatal("SetActive with an unregistered name should error, not silently no-op")
	}
	// The active backend must be unchanged after a bad SetActive.
	if r.ActiveName() != "huggingface" {
		t.Errorf("active changed to %q after a failed SetActive", r.ActiveName())
	}
}

func TestRegistry_Available(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeBackend{name: "ollama"})
	r.Register(fakeBackend{name: "huggingface"})
	got := r.Available()
	if len(got) != 2 || got[0] != "huggingface" || got[1] != "ollama" {
		t.Errorf("Available() = %v, want [huggingface ollama] sorted", got)
	}
}
