package runner

import (
	"testing"
)

// The DeepInfra index publishes a 256K window for this model; the per-family
// name table knows nothing about it and defaults to 128K. Before provider
// metadata reached the runner, every request against such a model was budgeted
// against the wrong denominator.
const (
	discoveredWindow = 262_144
	defaultedWindow  = 128_000
)

func TestKnownContextWindowFor_UsesProviderPublishedCapacity(t *testing.T) {
	c := &Core{d: Deps{
		CloudContextWindow: func(model string) (int, bool) {
			if model == "Qwen/Qwen3.5-35B-A3B" {
				return discoveredWindow, true
			}
			return 0, false
		},
	}}
	window, known := c.knownContextWindowFor(true, "Qwen/Qwen3.5-35B-A3B")
	if window != discoveredWindow || !known {
		t.Fatalf("got %d/%v, want %d/true", window, known, discoveredWindow)
	}
}

// No evidence must leave the existing conventional fallback untouched — this
// change adds provider truth, it does not remove the operational default.
func TestKnownContextWindowFor_FallsBackWhenUnknown(t *testing.T) {
	c := &Core{d: Deps{
		CloudContextWindow: func(string) (int, bool) { return 0, false },
	}}
	window, known := c.knownContextWindowFor(true, "some/unlisted-model")
	if window != defaultedWindow || known {
		t.Fatalf("got %d/%v, want %d/false", window, known, defaultedWindow)
	}
}

// A nil resolver (worker without evidence, test wiring) behaves exactly as
// before the change.
func TestKnownContextWindowFor_NilResolverKeepsPreviousBehavior(t *testing.T) {
	c := &Core{d: Deps{}}
	window, known := c.knownContextWindowFor(true, "claude-sonnet-4-6")
	if window != 200_000 || !known {
		t.Fatalf("got %d/%v, want 200000/true", window, known)
	}
}

// Invalid/zero provider metadata must not be treated as a real capacity.
func TestKnownContextWindowFor_ZeroCapacityIsNotAdopted(t *testing.T) {
	c := &Core{d: Deps{
		CloudContextWindow: func(string) (int, bool) { return 0, true },
	}}
	window, known := c.knownContextWindowFor(true, "some/unlisted-model")
	if window != defaultedWindow || known {
		t.Fatalf("got %d/%v, want %d/false", window, known, defaultedWindow)
	}
}

// Failover changes the destination model mid-turn; each attempt must budget
// against the model it actually targets.
func TestKnownContextWindowFor_DestinationModelDecidesBudget(t *testing.T) {
	c := &Core{d: Deps{
		CloudContextWindow: func(model string) (int, bool) {
			switch model {
			case "primary/big":
				return 1_048_576, true
			case "backup/small":
				return 32_768, true
			}
			return 0, false
		},
	}}
	if w, _ := c.knownContextWindowFor(true, "primary/big"); w != 1_048_576 {
		t.Fatalf("primary window = %d", w)
	}
	if w, _ := c.knownContextWindowFor(true, "backup/small"); w != 32_768 {
		t.Fatalf("backup window = %d, want the destination model's own capacity", w)
	}
}

// Local runtimes stay authoritative: a launched --ctx-size is a hard ceiling
// that provider metadata must never raise.
func TestKnownContextWindowFor_LocalIgnoresCloudEvidence(t *testing.T) {
	c := &Core{d: Deps{
		CloudContextWindow: func(string) (int, bool) { return 1_048_576, true },
	}}
	window, _ := c.knownContextWindowFor(false, "local-model")
	if window == 1_048_576 {
		t.Fatal("local execution adopted cloud context evidence")
	}
}
