package slash

// RegisterModel exposes a direct escape hatch: a broken pinned provider must
// never prevent the user from clearing or replacing their session override.
func RegisterModel(r *Registry) {
	r.Register(Command{Name: "model", Help: "Session main-chat model: /model [models <profile> | set <profile> <model-id> | clear]", Handler: func(args []string) Result {
		if len(args) == 0 || (len(args) == 1 && args[0] == "status") {
			return Result{Kind: ResultSessionModel, ModelAction: "status"}
		}
		if len(args) == 1 && args[0] == "clear" {
			return Result{Kind: ResultSessionModel, ModelAction: "clear"}
		}
		if len(args) == 2 && args[0] == "models" {
			return Result{Kind: ResultSessionModel, ModelAction: "models", ModelProfile: args[1]}
		}
		if len(args) == 3 && args[0] == "set" {
			return Result{Kind: ResultSessionModel, ModelAction: "set", ModelProfile: args[1], ModelID: args[2]}
		}
		return Result{Kind: ResultText, Text: "Usage: /model [status | models <profile> | set <saved-profile> <exact-model-id> | clear]"}
	}})
}
