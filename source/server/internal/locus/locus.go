// Package locus is the single source of truth for whether work runs on the
// local or cloud tier. It resolves a Mode into a preferred/fallback Tier; the
// caller maps Tiers to concrete providers and enforces availability.
package locus

import "fmt"

type Mode string

const (
	CloudOnly    Mode = "cloud_only"
	CloudPrimary Mode = "cloud_primary"
	OpenPrimary Mode = "open_primary"
	OpenOnly    Mode = "open_only"
)

// DefaultMode preserves Cercano's local-first intent.
const DefaultMode = OpenPrimary

type Tier int

const (
	TierLocal Tier = iota
	TierCloud
)

func (t Tier) String() string {
	if t == TierCloud {
		return "cloud"
	}
	return "local"
}

// Resolution describes how to serve a request for one work tier: the Preferred
// provider tier, the Fallback tier to use if Preferred can't serve, and whether
// crossing to the Fallback is allowed at all (false for the *_only modes).
type Resolution struct {
	Preferred    Tier
	Fallback     Tier
	CrossAllowed bool
}

// Main resolves the tier policy for the agent's main LLM.
func (m Mode) Main() Resolution {
	switch m {
	case CloudOnly:
		return Resolution{TierCloud, TierCloud, false}
	case CloudPrimary:
		return Resolution{TierCloud, TierLocal, true}
	case OpenOnly:
		return Resolution{TierLocal, TierLocal, false}
	case OpenPrimary:
		fallthrough
	default:
		return Resolution{TierLocal, TierCloud, true}
	}
}

// Coproc resolves the tier policy for one-shot co-processor work (summarize,
// extract, classify, explain, …). Identical to Main except Cloud Primary, which
// keeps grunt work local while the main LLM runs on cloud.
func (m Mode) Coproc() Resolution {
	if m == CloudPrimary {
		return Resolution{TierLocal, TierCloud, true}
	}
	return m.Main()
}

// ParseMode validates a config string. Empty resolves to DefaultMode.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "":
		return DefaultMode, nil
	case CloudOnly, CloudPrimary, OpenPrimary, OpenOnly:
		return Mode(s), nil
	default:
		return "", fmt.Errorf("invalid locus_mode %q (want cloud_only|cloud_primary|local_primary|local_only)", s)
	}
}
