package inference

import (
	"fmt"

	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

// Role selects which locus policy governs provider choice.
type Role int

const (
	RoleMain   Role = iota // main agentic work: mode.Main()
	RoleCoproc             // co-processor / one-shot work: mode.Coproc()
)

// Tiers holds the candidate inference providers; either may be nil/absent.
type Tiers struct {
	// Mode and ModelFor are frozen with task assignments in the candidate graph.
	Mode      locus.Mode
	ModelFor  func(Selection, config.Tier) string
	OpenReady func(string) bool
	// ResolveDestination is captured with TaskFor from the same routing graph.
	ResolveDestination func(config.Destination) (config.Destination, error)
	TaskFor            func(config.Task) config.TaskAssignment
	Cloud              Provider
	Open               Provider
	Destinations       map[config.Destination]Candidate
}

// Candidate retains logical profile identity separately from physical placement.
type Candidate struct {
	Provider Provider
	Profile  string
	IsCloud  bool
}

// Selection is the resolved provider for a unit of work.
type Selection struct {
	// PolicyDestination is the final logical route before Primary locus placement.
	PolicyDestination config.Destination
	Provider          Provider
	Destination       config.Destination
	Profile           string
	IsCloud           bool
	FellBack          bool
	Notice            string
}

// Router selects an inference provider from typed tiers under locus policy.
// It knows tier policy only; backend names stay at assembly.
type Router struct {
	Tiers Tiers
}

// Select resolves a provider under the given locus mode and role. Locus is the
// hard governor: cloud_only/local_only never cross tiers; preferred/fallback
// are honored only when the resolution permits crossing.
func (r Router) Select(mode locus.Mode, role Role) (Selection, error) {
	res := mode.Main()
	if role == RoleCoproc {
		res = mode.Coproc()
	}
	pick := func(t locus.Tier) Provider {
		if t == locus.TierCloud {
			if r.Tiers.Cloud != nil && r.Tiers.Cloud.Name() != "NONE" {
				return r.Tiers.Cloud
			}
			return nil
		}
		return r.Tiers.Open
	}
	label := "main"
	if role == RoleCoproc {
		label = "co-processor"
	}
	if prov := pick(res.Preferred); prov != nil {
		return Selection{Provider: prov, IsCloud: res.Preferred == locus.TierCloud}, nil
	}
	if res.CrossAllowed {
		if prov := pick(res.Fallback); prov != nil {
			return Selection{
				Provider: prov,
				IsCloud:  res.Fallback == locus.TierCloud,
				FellBack: true,
				Notice:   fmt.Sprintf("locus: preferred %s tier unavailable — ran on %s (%s)", label, res.Fallback, prov.Name()),
			}, nil
		}
	}
	return Selection{}, fmt.Errorf("locus mode %q: no %s provider available for %s work", mode, res.Preferred, label)
}

// Select is a convenience wrapper for one-shot callers.
func Select(mode locus.Mode, role Role, tiers Tiers) (Selection, error) {
	return Router{Tiers: tiers}.Select(mode, role)
}

// SelectDestination never treats Primary backup as Secondary. Explicit Secondary
// has no cross-destination fallback. Primary keeps the established locus policy.
func SelectDestination(mode locus.Mode, destination config.Destination, tiers Tiers) (Selection, error) {
	if tiers.Mode != "" {
		mode = tiers.Mode
	}
	if tiers.ResolveDestination != nil {
		final, err := tiers.ResolveDestination(destination)
		if err != nil {
			return Selection{}, err
		}
		destination = final
	}
	if destination == config.DestinationPrimary {
		selected, err := Select(mode, RoleMain, tiers)
		if err != nil {
			return Selection{}, err
		}
		selected.PolicyDestination = destination
		selected.Destination = config.DestinationLocal
		if selected.IsCloud {
			selected.Destination = destination
			if candidate, ok := tiers.Destinations[destination]; ok {
				selected.Profile = candidate.Profile
			}
		}
		return selected, nil
	}
	var candidate Candidate
	switch destination {
	case config.DestinationLocal:
		candidate = Candidate{Provider: tiers.Open}
	case config.DestinationSecondary:
		candidate = tiers.Destinations[destination]
	default:
		return Selection{}, fmt.Errorf("unknown destination %q", destination)
	}
	if candidate.Provider == nil || candidate.Provider.Name() == "NONE" {
		return Selection{}, fmt.Errorf("destination %s unavailable", destination)
	}
	if (mode == locus.OpenOnly && candidate.IsCloud) || (mode == locus.CloudOnly && !candidate.IsCloud) {
		return Selection{}, fmt.Errorf("locus mode %q prohibits destination %s placement", mode, destination)
	}
	return Selection{PolicyDestination: destination, Provider: candidate.Provider, Destination: destination, Profile: candidate.Profile, IsCloud: candidate.IsCloud}, nil
}
