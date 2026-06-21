package agenttools

// DefaultRegistry returns a Registry pre-populated with every R-tier
// (read-only) Tool. Used by cmd/cercano to wire the agent's tool surface
// at startup. W/X-tier tools (write_file, rm_file, git_push, etc.) are
// added in a follow-up chunk once the CLI's confirm-prompt UI lands.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.MustRegister(ReadFile())
	r.MustRegister(ListDir())
	r.MustRegister(StatFile())
	r.MustRegister(Grep())
	r.MustRegister(GitStatus())
	r.MustRegister(GitLog())
	return r
}
