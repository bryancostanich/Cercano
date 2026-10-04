package managedrouting

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

type Request struct {
	Model string
	Tier  config.Tier
	// LocalOnly preserves the meaning of explicit local co-processor tools.
	LocalOnly bool
}

// Select returns managed=false without touching standalone routing. Managed
// selection uses only the pinned plan and explicitly unlocked personal choices.
func Select(ctx context.Context, task config.Task, tiers inference.Tiers, request Request) (sel inference.Selection, assignment config.TaskAssignment, model string, managed bool, err error) {
	tiers = candidatesFor(ctx, tiers)
	plan, managed, err := managedsettings.PlanRoute(ctx, string(task), managedsettings.RouteOverride{})
	if !managed {
		if modelpolicy.Managed(ctx) {
			return sel, assignment, "", true, modelpolicy.Deny(modelpolicy.Attempt{}, "managed work has no pinned routing defaults")
		}
		return sel, assignment, "", false, nil
	}
	deny := func(e error) (inference.Selection, config.TaskAssignment, string, bool, error) {
		return sel, assignment, "", true, modelpolicy.Deny(modelpolicy.Attempt{}, e.Error())
	}
	if err != nil {
		return deny(err)
	}
	var preference managedsettings.RouteOverride
	if plan.AllowDeveloperOverride {
		preference, err = developerPreference(ctx, task, tiers, plan, request)
		if err != nil {
			return deny(err)
		}
	} else if request.Model != "" && request.Model != plan.Routes[0].Model {
		return deny(fmt.Errorf("administrator locked the model for task %q", task))
	}
	plan, _, err = managedsettings.PlanRoute(ctx, string(task), preference)
	if err != nil {
		return deny(err)
	}
	if request.LocalOnly {
		if plan.Routes[0].Placement != "local" {
			return deny(fmt.Errorf("local tool needs an approved local default for task %q", task))
		}
		local := plan.Routes[:0]
		for _, route := range plan.Routes {
			if route.Placement == "local" {
				local = append(local, route)
			}
		}
		plan.Routes = local
	}
	if tiers.ManagedRoute == nil {
		return deny(fmt.Errorf("managed provider binding is unavailable"))
	}
	assignment = config.TaskAssignment{Destination: config.Destination(plan.Destination), Quality: config.CostTier(plan.Quality)}
	provider, err := Build(ctx, plan, func(ctx context.Context, route v1.Route) (inference.Candidate, error) {
		return tiers.ManagedRoute(ctx, route, assignment.Destination)
	})
	if err != nil {
		return deny(err)
	}
	model = plan.Routes[0].Model
	// Resolve a supervised runtime before the caller budgets its prompt. Only
	// the explicit managed chain may supply a startup fallback.
	if _, err = llm.ResolveRuntimeContext(ctx, provider, model, true); err != nil {
		return sel, assignment, "", true, err
	}
	target := inference.TargetForCall(provider, inference.Call{Model: model})
	model = target.Model
	sel = inference.Selection{Provider: inference.WithTaskRoute(provider, task, assignment, assignment.Destination, model), PolicyDestination: assignment.Destination, Destination: config.Destination(target.Destination), Profile: target.Profile, IsCloud: target.Destination != "local", FellBack: provider.(*chain).preparedFallback.Load()}
	return sel, assignment, model, true, nil
}

func developerPreference(ctx context.Context, task config.Task, tiers inference.Tiers, plan managedsettings.RoutePlan, request Request) (managedsettings.RouteOverride, error) {
	preference := managedsettings.RouteOverride{}
	destination, quality := config.Destination(plan.Destination), config.CostTier(plan.Quality)
	explicit := false
	if c := tiers.DeveloperConfig; c != nil {
		if saved, ok := c.TaskAssignments[task]; ok {
			explicit = true
			if saved.Destination != "" {
				destination = saved.Destination
			}
			if saved.Quality != "" {
				quality = saved.Quality
			}
		}
	}
	if request.Tier != "" {
		if q, ok := config.CostTierForCapability(request.Tier); ok {
			quality = q
			explicit = true
		}
	}
	if request.Model != "" {
		explicit = true
	}
	if !explicit {
		return preference, nil
	}
	preference.Destination = string(destination)
	preference.Quality = string(quality)
	// A one-off model change without a saved task route stays on the approved
	// endpoint; naming a model alone cannot choose an unrelated service.
	c := tiers.DeveloperConfig
	if c == nil || (c.TaskAssignments[task].Destination == "" && c.TaskAssignments[task].Quality == "") {
		route := plan.Routes[0]
		if request.Model != "" {
			route.Model = request.Model
		}
		preference.Route = &route
		return preference, nil
	}
	final, err := c.ResolveDestination(destination)
	if err != nil {
		return preference, err
	}
	preference.Destination = string(final)
	model := request.Model
	local := final == config.DestinationLocal || (final == config.DestinationPrimary && (c.LocusMode == "open_only" || c.LocusMode == "open_primary"))
	if local {
		if model == "" && task == config.TaskCompaction && final == config.DestinationLocal {
			model = c.Compaction.SummarizerModel
		}
		if model == "" && tiers.ModelFor != nil {
			model = tiers.ModelFor(inference.Selection{}, quality.CapabilityTier())
		}
		if c.OpenRuntime == "ollama" {
			endpoint := strings.TrimRight(c.OllamaURL, "/")
			parsed, _ := url.Parse(endpoint)
			preference.Route = &v1.Route{Provider: "ollama", Endpoint: endpoint, Model: model, Placement: modelpolicy.Placement(parsed)}
			return preference, nil
		}
		snapshot, _ := managedsettings.FromContext(ctx)
		for _, route := range snapshot.Policy.AllowedRoutes {
			if route.Provider == c.OpenRuntime && route.Model == model && route.Placement == "local" {
				if preference.Route != nil {
					return preference, fmt.Errorf("local developer preference has ambiguous approved endpoints")
				}
				copy := route
				preference.Route = &copy
			}
		}
		if preference.Route == nil {
			return preference, fmt.Errorf("developer's local model has no approved route")
		}
		return preference, nil
	}
	name, _ := c.DestinationProfiles(final)
	profile, ok := c.Profile(name)
	if !ok {
		return preference, fmt.Errorf("developer's selected profile is unavailable")
	}
	provider, endpoint, err := cloudfactory.PhysicalEndpoint(ctx, profile)
	if err != nil {
		return preference, err
	}
	if model == "" {
		model = c.ModelProfiles.ResolveCloudModelForTier(profile, quality.CapabilityTier())
	}
	preference.Route = &v1.Route{Provider: provider, Endpoint: endpoint, Model: model, Placement: "external"}
	return preference, nil
}
