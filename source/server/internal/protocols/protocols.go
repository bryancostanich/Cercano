// Package protocols is the single source for Cercano's workflow protocols.
// One protocol document feeds three outputs: the always-on steering block
// (Trigger lines), the get_protocol capability (Body), and generated SKILL.md
// files for host discovery. Ported from the hardwAIr_hckr "Dave" core skills
// and the generic khalkulo/workflow decision protocol.
package protocols

import "sort"

// Domain separates generic protocols from domain-specific ones so the default
// steering set can exclude hardware protocols.
type Domain string

const (
	DomainCore     Domain = "core"
	DomainHardware Domain = "hardware"
)

// Protocol is one workflow discipline.
type Protocol struct {
	Name        string // kebab-case id, e.g. "design-decisions"
	Description string // one-line, for skill discovery
	Domain      Domain
	Trigger     string // one-line always-on rule (feeds the steering block)
	Body        string // the full protocol markdown (pulled on demand)
}

// Builtins returns the built-in protocol catalog, sorted by name.
func Builtins() []Protocol {
	out := append([]Protocol(nil), builtinProtocols...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a protocol by name.
func Get(name string) (Protocol, bool) {
	for _, p := range builtinProtocols {
		if p.Name == name {
			return p, true
		}
	}
	return Protocol{}, false
}

// ForDomain returns protocols in the given domain, sorted by name.
func ForDomain(d Domain) []Protocol {
	var out []Protocol
	for _, p := range Builtins() {
		if p.Domain == d {
			out = append(out, p)
		}
	}
	return out
}
