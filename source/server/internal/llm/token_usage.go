package llm

// TokenCount distinguishes a provider-reported zero from an unavailable count.
// Value is meaningful only when Known is true. Counts are consumed tokens, not
// context estimates, capacity, reservations, or estimated savings.
type TokenCount struct {
	Value int64 `json:"value"`
	Known bool  `json:"known"`
}

func ReportedTokens(n int64) TokenCount {
	if n < 0 {
		return TokenCount{}
	}
	return TokenCount{Value: n, Known: true}
}

// TokenUsage is a cumulative snapshot for ONE physical inference attempt.
// Input includes cache reads/writes; Output includes reasoning when the provider
// reports that convention. Cache/reasoning are breakdowns, never added again to
// Input+Output. An adapter must normalize additive provider categories first.
// These value-only fields are safe to copy into an immutable queued observation.
type TokenUsage struct {
	// Final means usage was reported at a provider-defined final usage boundary.
	// False means finality is unproven, not that the reported counts are invalid.
	// Execution completion alone never establishes this evidence.
	Final      bool       `json:"final"`
	Input      TokenCount `json:"input"`
	Output     TokenCount `json:"output"`
	CacheRead  TokenCount `json:"cache_read"`
	CacheWrite TokenCount `json:"cache_write"`
	Reasoning  TokenCount `json:"reasoning"`
}

// Merge replaces reported fields, including legitimate zero. Missing fields do
// not erase previously observed counts. It does not sum repeated snapshots.
func (u TokenUsage) Merge(next TokenUsage) TokenUsage {
	u.Final = u.Final || next.Final
	if next.Input.Known {
		u.Input = next.Input
	}
	if next.Output.Known {
		u.Output = next.Output
	}
	if next.CacheRead.Known {
		u.CacheRead = next.CacheRead
	}
	if next.CacheWrite.Known {
		u.CacheWrite = next.CacheWrite
	}
	if next.Reasoning.Known {
		u.Reasoning = next.Reasoning
	}
	return u
}

func (u TokenUsage) TotalsKnown() bool { return u.Input.Known && u.Output.Known }

// Complete requires both final-usage evidence and known inclusive totals.
func (u TokenUsage) Complete() bool { return u.Final && u.TotalsKnown() }

// Reported distinguishes observed usage from an empty/SDK-ambiguous snapshot.
func (u TokenUsage) Reported() bool {
	return u.Input.Known || u.Output.Known || u.CacheRead.Known || u.CacheWrite.Known || u.Reasoning.Known
}
