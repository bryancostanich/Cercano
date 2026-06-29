package builtins

import (
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestRegister_Count(t *testing.T) {
	reg := capabilities.NewRegistry(capabilities.Services{})
	Register(reg)
	all := reg.All()
	if len(all) != 20 {
		names := make([]string, len(all))
		for i, c := range all {
			names[i] = c.Name()
		}
		t.Fatalf("expected 20 capabilities, got %d: %v", len(all), names)
	}
}

func TestAgentAliases_Count(t *testing.T) {
	aliases := AgentAliases()
	want := 7
	if len(aliases) != want {
		t.Fatalf("expected %d aliases, got %d: %v", want, len(aliases), aliases)
	}
}

func TestAgentAliases_Entries(t *testing.T) {
	aliases := AgentAliases()
	expected := map[string]string{
		"read_file":   "Read",
		"list_dir":    "LS",
		"glob":        "Glob",
		"grep":        "Grep",
		"write_file":  "Write",
		"edit_file":   "Edit",
		"run_command": "Bash",
	}
	for k, v := range expected {
		got, ok := aliases[k]
		if !ok {
			t.Errorf("missing alias for %q", k)
			continue
		}
		if got != v {
			t.Errorf("alias[%q] = %q, want %q", k, got, v)
		}
	}
}
