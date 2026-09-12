package ui

import (
	"cercano/source/server/pkg/agentclient"
	"strings"
	"testing"
	"time"
)

func TestRuntimeContextHintDistinguishesPlannedAndConfirmed(t *testing.T) {
	instance := agentclient.RuntimeInstance{Runtime: "llama_server", State: "running", PlannedContextTokens: 65536, PlannedContextSource: "ram_profile", ConfirmedContextTokens: 8192, ContextConfirmedAt: time.Now()}
	if hint := instanceHint(instance); !strings.Contains(hint, "context:8192 confirmed") {
		t.Fatalf("confirmed capacity missing: %q", hint)
	}
	instance.State = "starting"
	instance.ConfirmedContextTokens = 0
	if hint := instanceHint(instance); !strings.Contains(hint, "context:65536 planned (ram_profile); unconfirmed") {
		t.Fatalf("planned capacity mislabeled: %q", hint)
	}
	instance.PlannedContextTokens = 0
	if hint := instanceHint(instance); !strings.Contains(hint, "context:unknown") {
		t.Fatalf("unknown capacity missing: %q", hint)
	}
}
