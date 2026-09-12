package config

import (
	cfg "cercano/source/server/pkg/config"
	"testing"
)

func TestContextOverrideSnapshotsAndValidation(t *testing.T) {
	n := 8192
	c := cfg.Defaults()
	c.LlamaServer.ContextSize = &n
	s := New("", c, nil)
	n = 1
	if s.Get().LlamaServer.ContextOverride() != 8192 {
		t.Fatal("constructor aliases caller")
	}
	out := s.Get()
	*out.LlamaServer.ContextSize = 2
	if s.Get().LlamaServer.ContextOverride() != 8192 {
		t.Fatal("Get aliases snapshot")
	}
	n = 65536
	c.LlamaServer.ContextSize = &n
	if err := s.Set(c); err != nil {
		t.Fatal(err)
	}
	n = 3
	if s.Get().LlamaServer.ContextOverride() != 65536 {
		t.Fatal("Set aliases caller")
	}
	for _, n := range []int{0, -1} {
		c.LlamaServer.ContextSize = &n
		if err := s.Set(c); err == nil {
			t.Fatal("Set accepted nonpositive override")
		}
		if s.Get().LlamaServer.ContextOverride() != 65536 {
			t.Fatal("invalid Set changed state")
		}
	}
}

func TestContextOverrideMutateRejectsInvalidState(t *testing.T) {
	s := New("", cfg.Defaults(), nil)
	if err := s.Mutate(func(c *cfg.Config) { n := -1; c.LlamaServer.ContextSize = &n }); err == nil {
		t.Fatal("Mutate must report validation error")
	}
	if s.Get().LlamaServer.ContextSize != nil {
		t.Fatal("Mutate installed invalid context override")
	}
}
