package slash

// RegisterSetup installs /setup, which opens the guided setup wizard page
// (primary model, cloud auth, locus mode, model tiers). The same page opens
// on first run and via the -s / -setup launch flags.
func RegisterSetup(r *Registry) {
	r.Register(Command{
		Name: "setup",
		Help: "Open the setup wizard.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenWizard}
		},
	})
}
