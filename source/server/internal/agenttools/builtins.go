package agenttools

// DefaultRegistry returns a Registry pre-populated with every built-in
// Tool — R-tier read-only, W-tier writes, and X-tier destructive. The CLI
// confirm-prompt UI gates W and X tiers; R-tier runs silently.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	// R-tier (read-only)
	r.MustRegister(ReadFile())
	r.MustRegister(ListDir())
	r.MustRegister(StatFile())
	r.MustRegister(Grep())
	r.MustRegister(GitStatus())
	r.MustRegister(GitLog())
	// W-tier (writes; CLI confirms before running)
	r.MustRegister(WriteFile())
	r.MustRegister(EditFile())
	r.MustRegister(RunCommand())
	r.MustRegister(GitAdd())
	r.MustRegister(GitCommit())
	// X-tier (destructive; CLI always confirms with extra emphasis)
	r.MustRegister(RmFile())
	r.MustRegister(GitPush())
	r.MustRegister(GitResetHard())
	return r
}
