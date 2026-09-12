package builtins

import (
	"context"
	"testing"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/pkg/config"
)

// These producer controls cover migrated text analysis and explicit local intent.
// Effective routing under redirects still needs coverage once redirects exist.
func TestTaskTaxonomyExcludedProducerIntent(t *testing.T) {
	t.Run("coprocessor", func(t *testing.T) {
		svc, got := fakeDispatch(t, "ok")
		_, err := runTextAnalysis(context.Background(), callWith(t, svc, nil), "summarize", "summarize this", "text")
		if err != nil {
			t.Fatal(err)
		}
		if got.Mode != dispatch.OneShot || got.Role != dispatch.RoleCoproc || got.Tier != "" || got.ModelOverride != "" || got.RoutingTask != config.TaskReconnaissance {
			t.Fatalf("text analysis not classified as Reconnaissance: %+v", got)
		}
	})
	for _, model := range []string{"", "explicit-local-model"} {
		t.Run("local/model="+model, func(t *testing.T) {
			svc, got := fakeDispatch(t, "ok")
			_, err := Local().Execute(context.Background(), callWith(t, svc, map[string]string{"prompt": "local work", "model": model}))
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != dispatch.OneShot || got.Role != dispatch.RoleCoproc || got.Tier != config.TierEveryday || got.ModelOverride != model || got.RoutingTask != "" || got.Source != "local" {
				t.Fatalf("excluded local intent changed: %+v", got)
			}
		})
	}
}
