package builtins

import (
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/mcpadapter"
)

func init() {
	capabilities.RegisterMCPCatalogSource(func() []mcpadapter.CapMeta {
		reg := capabilities.NewRegistry(capabilities.Services{})
		Register(reg)
		var out []mcpadapter.CapMeta
		for _, c := range reg.ForSurface(capabilities.SurfaceMCP) {
			out = append(out, mcpadapter.CapMeta{
				Name:        c.Name(),
				Description: c.Description(),
				Schema:      c.Schema(),
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
	// W-tier
	reg.MustRegister(WriteFile())
	reg.MustRegister(EditFile())
	reg.MustRegister(RunCommand())
	reg.MustRegister(GitAdd())
	reg.MustRegister(GitCommit())
	// X-tier
	reg.MustRegister(RmFile())
	reg.MustRegister(GitPush())
	reg.MustRegister(GitResetHard())
}

// AgentAliases maps canonical capability names to standalone display names.
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
