package managedsettings_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

func routeFixture(unlocked bool) managedsettings.Snapshot {
	s := settingstest.Snapshot("company-a", "1", "Review carefully.")
	s.Policy.AllowedRoutes = append(s.Policy.AllowedRoutes,
		v1.Route{ID: "backup-b", Provider: "anthropic", Endpoint: "https://api.anthropic.com", Model: "backup-b", Placement: "external"},
		v1.Route{ID: "backup-c", Provider: "openai", Endpoint: "https://other.example/v1", Model: "backup-c", Placement: "external"},
		v1.Route{ID: "local", Provider: "ollama", Endpoint: "http://127.0.0.1:11434", Model: "local", Placement: "local"})
	s.Policy.TaskDefaults[0].FallbackRouteIDs = []string{"backup-b", "local", "backup-c"}
	s.Policy.TaskDefaults[0].AllowDeveloperOverride = unlocked
	return s
}
func TestManagedRoutePlanUsesOnlyExplicitOrderedFallbacks(t *testing.T) {
	s := routeFixture(false)
	ctx := managedsettings.WithSnapshot(context.Background(), s)
	p, managed, err := managedsettings.PlanRoute(ctx, "chat", managedsettings.RouteOverride{})
	if err != nil || !managed {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range p.Routes {
		ids = append(ids, r.ID)
	}
	if !reflect.DeepEqual(ids, []string{"approved", "backup-b", "local", "backup-c"}) || p.Quality != "standard" || p.Destination != "primary" {
		t.Fatalf("unexpected plan: %+v", p)
	}
	p.Routes[0].Model = "mutated"
	again, _, _ := managedsettings.PlanRoute(ctx, "chat", managedsettings.RouteOverride{})
	if again.Routes[0].Model != "approved" {
		t.Fatal("route plan mutated turn snapshot")
	}
}
func TestManagedRoutePlanLocksAndUnlocksDeveloperPreferences(t *testing.T) {
	for _, unlocked := range []bool{false, true} {
		s := routeFixture(unlocked)
		ctx := managedsettings.WithSnapshot(context.Background(), s)
		route := s.Policy.AllowedRoutes[1]
		p, _, err := managedsettings.PlanRoute(ctx, "chat", managedsettings.RouteOverride{Route: &route, Quality: "premium", Destination: "secondary"})
		if !unlocked {
			if err == nil || !strings.Contains(err.Error(), "locked") {
				t.Fatalf("locked defaults accepted: %v", err)
			}
			continue
		}
		if err != nil || p.Routes[0].ID != "backup-b" || p.Quality != "premium" || p.Destination != "secondary" {
			t.Fatalf("override=%+v err=%v", p, err)
		}
		if len(p.Routes) != 3 || p.Routes[1].ID != "local" || p.Routes[2].ID != "backup-c" {
			t.Fatal("override invented/reordered fallback routes")
		}
	}
}
func TestUnlockedPreferenceCannotBroadenPermission(t *testing.T) {
	s := routeFixture(true)
	ctx := managedsettings.WithSnapshot(context.Background(), s)
	for _, field := range []string{"provider", "endpoint", "model", "placement"} {
		route := s.Policy.AllowedRoutes[0]
		switch field {
		case "provider":
			route.Provider = "other"
		case "endpoint":
			route.Endpoint = "https://evil.example"
		case "model":
			route.Model = "other"
		case "placement":
			route.Placement = "local"
		}
		if _, _, err := managedsettings.PlanRoute(ctx, "chat", managedsettings.RouteOverride{Route: &route}); err == nil {
			t.Fatalf("changed %s accepted", field)
		}
	}
	local := s.Policy.AllowedRoutes[3]
	plan, _, err := managedsettings.PlanRoute(ctx, "chat", managedsettings.RouteOverride{Route: &local, Destination: "local"})
	if err != nil || len(plan.Routes) != 1 || plan.Routes[0].Placement != "local" {
		t.Fatalf("local override leaked to cloud: %+v %v", plan, err)
	}
}
func TestUnassignedManagedTaskDoesNotInheritPersonalDefaults(t *testing.T) {
	ctx := managedsettings.WithSnapshot(context.Background(), routeFixture(false))
	if _, managed, err := managedsettings.PlanRoute(ctx, "compaction", managedsettings.RouteOverride{}); !managed || err == nil {
		t.Fatal("unassigned task silently inherited personal settings")
	}
	if _, managed, err := managedsettings.PlanRoute(context.Background(), "chat", managedsettings.RouteOverride{}); managed || err != nil {
		t.Fatal("standalone behavior changed")
	}
}
