package builtins

import (
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/mcpadapter"
)

func init() {
	capabilities.RegisterMCPCatalogSource(func() []mcpadapter.CapMeta {
		reg := capabilities.NewRegistry(capabilities.Services{})
		Register(reg)
		syns := CapabilitySynonyms()
		var out []mcpadapter.CapMeta
		for _, c := range reg.ForSurface(capabilities.SurfaceMCP) {
			out = append(out, mcpadapter.CapMeta{
				Name:        c.Name(),
				Description: c.Description(),
				Schema:      c.Schema(),
				Synonyms:    syns[c.Name()],
			})
		}
		return out
	})
}

// Register adds every built-in capability to reg.
func Register(reg *capabilities.Registry) {
	// R-tier
	reg.MustRegister(ReadFile())
	reg.MustRegister(ListDir())
	reg.MustRegister(StatFile())
	reg.MustRegister(Glob())
	reg.MustRegister(Grep())
	reg.MustRegister(GitStatus())
	reg.MustRegister(GitLog())
	reg.MustRegister(GetProtocol())
	reg.MustRegister(Summarize())
	reg.MustRegister(Extract())
	reg.MustRegister(Classify())
	reg.MustRegister(Explain())
	reg.MustRegister(Review())
	// W-tier
	reg.MustRegister(WriteFile())
	reg.MustRegister(EditFile())
	reg.MustRegister(RunCommand())
	reg.MustRegister(GitAdd())
	reg.MustRegister(GitCommit())
	reg.MustRegister(Dispatch())
	reg.MustRegister(GitWorktree())
	reg.MustRegister(Checkpoint())
	reg.MustRegister(GitSquash())
	reg.MustRegister(GitBisect())
	// X-tier
	reg.MustRegister(RmFile())
	reg.MustRegister(GitPush())
	reg.MustRegister(GitResetHard())
	reg.MustRegister(GitRecover())
	reg.MustRegister(GitLand())
}

// AgentAliases maps canonical capability names to standalone display names —
// pure renames applied only on the standalone surface (e.g. so an LLM trained
// on Claude Code conventions reaches for "Read" instead of "read_file"). The
// canonical name is replaced by the display name in the standalone catalog;
// nothing is registered under the canonical name on that surface.
//
// For alternate names that should coexist with the canonical name on both
// surfaces, see CapabilitySynonyms.
func AgentAliases() map[string]string {
	return map[string]string{
		"read_file":   "Read",
		"list_dir":    "LS",
		"glob":        "Glob",
		"grep":        "Grep",
		"write_file":  "Write",
		"edit_file":   "Edit",
		"run_command": "Bash",
		// stat_file, git_*, rm_file keep their canonical names as display names.
	}
}

// CapabilitySynonyms maps a capability's canonical name to alternate names it
// should ALSO be discoverable under, on both the standalone and MCP surfaces.
// Unlike AgentAliases (a pure rename), synonyms are additive: the capability
// remains discoverable under its canonical name AND every synonym, so callers
// reaching for either name find the tool.
//
// Motivating case: `dispatch` — Cercano's canonical name — is what host models
// like Claude reach for as "workflow". Registering both means the tool is
// found no matter which name the model tries first.
func CapabilitySynonyms() map[string][]string {
	return map[string][]string{
		"dispatch": {"workflow"},
	}
}
