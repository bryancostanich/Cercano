package slash

// RegisterSettings installs /s and /settings, the sectioned settings page that
// replaces the old flat /config editor. /config (registered in config.go) opens
// the same page when called with no args.
func RegisterSettings(r *Registry) {
	r.Register(Command{
		Name:    "s",
		Aliases: []string{"settings"},
		Help:    "Open the settings page.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenSettings}
		},
	})
}
