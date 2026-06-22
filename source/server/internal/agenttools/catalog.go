package agenttools

import (
	"cercano/source/server/internal/llm"
)

func BuildToolCatalog(reg *Registry) []llm.Tool {
	src := reg.All()
	out := make([]llm.Tool, 0, len(src))
	for _, t := range src {
		out = append(out, llm.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
			Permission:  permissionToLLM(t.Permission()),
		})
	}
	return out
}

func permissionToLLM(p Permission) llm.Permission {
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
