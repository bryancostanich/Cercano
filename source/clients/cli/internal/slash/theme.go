package slash

// RegisterTheme installs /theme, which opens the settings page (where the theme
// selector + editor live).
func RegisterTheme(r *Registry) {
	r.Register(Command{
		Name: "theme",
		Help: "Open the settings page to switch or edit the color theme.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenThemeSettings}
		},
	})
}
