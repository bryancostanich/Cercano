package dispatch

import (
	"fmt"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
)

// Role selects which locus policy governs the choice.
type Role int

const (
	RoleMain   Role = iota // main agentic work: mode.Main()
	RoleCoproc             // co-processor / one-shot work: mode.Coproc()
)

// Providers holds the candidate providers; either may be nil/absent.
type Providers struct {
	Cloud llm.Provider
	Open  llm.Provider
}

// Selection is the resolved provider for a unit of work.
type Selection struct {
	Provider llm.Provider
	IsCloud  bool
	FellBack bool
	Notice   string
}

// Select resolves a provider under the given locus mode and role. Locus is the
// hard governor: cloud_only/local_only never cross tiers; preferred/fallback
// are honored only when the resolution permits crossing.
func Select(mode locus.Mode, role Role, p Providers) (Selection, error) {
	res := mode.Main()
	if role == RoleCoproc {
		res = mode.Coproc()
	}
	pick := func(t locus.Tier) llm.Provider {
		if t == locus.TierCloud {
			if p.Cloud != nil && p.Cloud.Name() != "NONE" {
				return p.Cloud
			}
			return nil
		}
		return p.Open
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
