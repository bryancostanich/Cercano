package slash

// RegisterRuntime installs /m, the local/external model runtime dashboard, and
// /runtime, the active-runtime + open-model-picker config tab.
func RegisterRuntime(r *Registry) {
	r.Register(Command{
		Name: "m",
		Help: "Open the model runtime dashboard.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenRuntimeDashboard}
		},
	})
	r.Register(Command{
		Name: "runtime",
		Help: "Open the runtime config tab (active runtime + open-model picker).",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenRuntimeConfig}
		},
	})
}
