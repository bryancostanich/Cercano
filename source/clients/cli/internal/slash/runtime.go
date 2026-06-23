package slash

// RegisterRuntime installs /m, the local/external model runtime dashboard.
func RegisterRuntime(r *Registry) {
	r.Register(Command{
		Name: "m",
		Help: "Open the model runtime dashboard.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenRuntimeDashboard}
		},
	})
}
