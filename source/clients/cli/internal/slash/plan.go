package slash

import (
	"cercano/source/server/pkg/agentclient"
)

// RegisterPlan wires /plan, the entrypoint to the read-only planning profile
// (and, generally, the active capability profile). The client parameter is
// accepted for parity with other Register* funcs; the UI model dispatches the
// SetSessionProfile RPC itself.
//
//	/plan        -> enter the read-only planning profile
//	/plan off    -> leave planning (return to the unrestricted default)
//	/plan <name> -> switch to a named profile (future modes)
//
// Distinct from /mode, which sets the permission mode (strict/permissive/
// bypass) — an orthogonal axis. Planning fences which tools exist at all;
// permission mode governs whether the agent asks before W/X tools.
func RegisterPlan(r *Registry, _ *agentclient.Client) {
	r.Register(Command{
		Name: "plan",
		Help: "Enter read-only planning mode: /plan (on) | /plan off | /plan <profile>.",
		Handler: func(args []string) Result {
			name := "plan"
			if len(args) > 0 {
				switch args[0] {
				case "off", "default", "none", "exit":
					name = "default"
				default:
					name = args[0]
				}
			}
			return Result{Kind: ResultSetSessionProfile, SessionProfile: name}
		},
	})
}
