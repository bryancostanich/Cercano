package dispatch

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/pkg/config"
	"context"
	"testing"
)

func TestMissingClassIgnoresLegacyRoleAndSource(t *testing.T) {
	for _, role := range []Role{0, RoleMain, RoleCoproc} {
		for _, source := range []string{"", "review", "summarize", "local"} {
			e, p, s, l := taxonomyEngine()
			spec := Spec{Role: role, Source: source, Prompt: "looks like research but is unclassified"}
			target, err := e.PreparedTarget(context.Background(), spec)
			if err != nil || target.Model != "secondary-everyday" {
				t.Fatalf("prepared target %+v %v", target, err)
			}
			_, err = e.Dispatch(context.Background(), spec)
			if err != nil || len(s.calls) != 1 || len(p.calls)+len(l.calls) != 0 {
				t.Fatalf("role=%v source=%q err=%v", role, source, err)
			}
			e.SetAgenticRunner(func(_ context.Context, got Spec, sel inference.Selection, model string) (Result, error) {
				if got.RoutingTask != config.TaskDispatch || got.Tier != config.TierEveryday || sel.Destination != config.DestinationSecondary || model != "secondary-everyday" {
					t.Errorf("agentic spec=%+v sel=%+v model=%s", got, sel, model)
				}
				return Result{Model: model}, nil
			})
			spec.Mode = Agentic
			if _, err = e.Dispatch(context.Background(), spec); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestLocalOffloadCannotCarryTaskAssignment(t *testing.T) {
	e, _, _, _ := taxonomyEngine()
	spec := Spec{LocalOffload: true, RoutingTask: config.TaskDispatch}
	if _, err := e.Target(spec); err == nil {
		t.Fatal("conflicting intent accepted")
	}
	if _, err := e.Dispatch(context.Background(), spec); err == nil {
		t.Fatal("conflicting intent executed")
	}
}
