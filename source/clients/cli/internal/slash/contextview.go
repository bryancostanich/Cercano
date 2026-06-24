package slash

// RegisterContextView installs the /c command, which opens the read-only
// context viewer for the active conversation.
func RegisterContextView(r *Registry) {
	r.Register(Command{
		Name: "c",
		Help: "Open the context viewer for the current conversation.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenContextView}
		},
	})
}
