package inference

import (
	"fmt"

	"cercano/source/server/internal/locus"
)

// Role selects which locus policy governs provider choice.
type Role int

const (
	RoleMain   Role = iota // main agentic work: mode.Main()
	RoleCoproc             // co-processor / one-shot work: mode.Coproc()
)

// Tiers holds the candidate inference providers; either may be nil/absent.
type Tiers struct {
	Cloud Provider
	Open  Provider
}

// Selection is the resolved provider for a unit of work.
type Selection struct {
	Provider Provider
	IsCloud  bool
	FellBack bool
	Notice   string
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
