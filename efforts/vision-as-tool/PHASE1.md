# Phase 1 seam map

See `plan.md` Phase 1 for the inline checklist and confirmed seams.

Key seams:

- Image path: proto inline images → `server.mapInlineImages` → runner request → `agent.RunToolLoop` → `agent.buildUserBlocks` → `llm.BlockImage`.
- Tool registration: `internal/capabilities/builtins/builtins.go:Register`.
- Tool execution: `agent.RunToolLoop` registry lookup, permission partition, `agenttools.Result` → `llm.BlockToolResult`.
- Model tiers: `pkg/config/models.go`, `internal/openmodels.Resolver.Model`, `server.resolveTierModel`, `providers.MainModel`.
- Locus: `internal/locus`; vision fallback should use `locus.ParseMode` and allow cloud for every mode except `OpenOnly`.
