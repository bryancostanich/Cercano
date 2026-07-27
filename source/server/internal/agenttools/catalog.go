package agenttools

import (
	"cercano/source/server/internal/llm"
)

func BuildToolCatalog(reg *Registry) []llm.Tool {
	return BuildToolCatalogFiltered(reg, nil)
}

// BuildToolCatalogFiltered is BuildToolCatalog with an optional allow predicate.
// When allow is non-nil, a tool is included only if allow(tier, name) is true —
// the advertisement half of a capability profile, so the model is never offered
// a tool the active profile forbids. A nil predicate includes every tool.
//
// The predicate is a plain func (not a Profile) so this package stays free of an
// agent-package import; the caller in agent passes Profile.Allows.
func BuildToolCatalogFiltered(reg *Registry, allow func(tier llm.Permission, name string) bool) []llm.Tool {
	src := reg.All()
	out := make([]llm.Tool, 0, len(src))
	for _, t := range src {
		tier := PermissionToLLM(t.Permission())
		if allow != nil && !allow(tier, t.Name()) {
			continue
		}
		out = append(out, llm.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
			Permission:  tier,
		})
	}
	return out
}

func PermissionToLLM(p Permission) llm.Permission {
	switch p {
	case PermR:
		return llm.PermR
	case PermW:
		return llm.PermW
	case PermX:
		return llm.PermX
	}
	return llm.PermR
}
