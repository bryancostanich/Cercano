package managedsettings

import (
	"context"
	"fmt"

	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

// RouteOverride is an explicit developer preference, resolved to a physical
// identity by the host's existing configuration. An empty value means use the
// administrator's default. It never adds a fallback route.
type RouteOverride struct {
	Route       *v1.Route
	Destination string
	Quality     string
}

// RoutePlan is an ordered list for one task in the pinned policy. Its routes
// still require the host's current authorization before every physical call.
type RoutePlan struct {
	Task                   string
	Destination            string
	Quality                string
	Routes                 []v1.Route
	AllowDeveloperOverride bool
}

// PlanRoute returns managed=false for standalone work. A managed task without
// an assignment fails visibly; personal defaults are not implicitly unlocked.
func PlanRoute(ctx context.Context, task string, override RouteOverride) (plan RoutePlan, managed bool, err error) {
	snapshot, managed := FromContext(ctx)
	if !managed {
		return RoutePlan{}, false, nil
	}
	var assignment *v1.TaskDefault
	for i := range snapshot.Policy.TaskDefaults {
		if snapshot.Policy.TaskDefaults[i].Task == task {
			assignment = &snapshot.Policy.TaskDefaults[i]
			break
		}
	}
	if assignment == nil {
		return plan, true, fmt.Errorf("enterprise administrator has not assigned a route for task %q", task)
	}
	plan = RoutePlan{Task: task, Destination: assignment.Destination, Quality: assignment.Quality, AllowDeveloperOverride: assignment.AllowDeveloperOverride}
	routes := make(map[string]v1.Route, len(snapshot.Policy.AllowedRoutes))
	for _, route := range snapshot.Policy.AllowedRoutes {
		routes[route.ID] = route
	}
	primary, ok := routes[assignment.RouteID]
	if !ok {
		return plan, true, fmt.Errorf("enterprise task %q references an unavailable route", task)
	}
	changed := (override.Route != nil && !samePhysicalRoute(primary, *override.Route)) || (override.Destination != "" && override.Destination != plan.Destination) || (override.Quality != "" && override.Quality != plan.Quality)
	if changed && !assignment.AllowDeveloperOverride {
		return plan, true, fmt.Errorf("enterprise administrator locked the defaults for task %q", task)
	}
	if override.Route != nil {
		found := false
		for _, route := range snapshot.Policy.AllowedRoutes {
			if samePhysicalRoute(route, *override.Route) {
				primary = route
				found = true
				break
			}
		}
		if !found {
			return plan, true, fmt.Errorf("developer route for task %q is outside the enterprise allow-list", task)
		}
	}
	if override.Destination != "" {
		plan.Destination = override.Destination
	}
	if override.Quality != "" {
		plan.Quality = override.Quality
	}
	if plan.Destination != "primary" && plan.Destination != "secondary" && plan.Destination != "local" {
		return plan, true, fmt.Errorf("invalid managed destination %q", plan.Destination)
	}
	if plan.Quality != "economy" && plan.Quality != "standard" && plan.Quality != "premium" {
		return plan, true, fmt.Errorf("invalid managed quality %q", plan.Quality)
	}
	if plan.Destination == "local" && primary.Placement != "local" {
		return plan, true, fmt.Errorf("local task %q cannot use an external route", task)
	}
	plan.Routes = []v1.Route{primary}
	for _, id := range assignment.FallbackRouteIDs {
		route, ok := routes[id]
		if !ok {
			return plan, true, fmt.Errorf("enterprise fallback %q is unavailable", id)
		}
		if samePhysicalRoute(primary, route) {
			continue
		}
		// An unlocked local-only preference can narrow the administrator's list,
		// but never silently send its work to an external fallback.
		if plan.Destination == "local" && route.Placement != "local" {
			continue
		}
		plan.Routes = append(plan.Routes, route)
	}
	return plan, true, nil
}

func samePhysicalRoute(a, b v1.Route) bool {
	return a.Provider == b.Provider && a.Endpoint == b.Endpoint && a.Model == b.Model && a.Placement == b.Placement
}
