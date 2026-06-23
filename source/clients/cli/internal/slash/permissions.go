package slash

import (
	"cercano/source/server/pkg/agentclient"
)

// RegisterPermissions wires /strict, /permissive, /bypass, and /mode. The
// client parameter is accepted for signature parity with other Register*
// funcs; the UI model dispatches the SetPermissionMode RPC itself.
func RegisterPermissions(r *Registry, _ *agentclient.Client) {
	for _, m := range []string{"strict", "permissive", "bypass"} {
		mode := m
		r.Register(Command{
			Name: mode,
			Help: "Set permission mode to " + mode + ".",
			Handler: func(args []string) Result {
				return Result{Kind: ResultSetPermissionMode, PermissionMode: mode}
			},
		})
	}
	r.Register(Command{
		Name: "mode",
		Help: "Set permission mode: /mode <strict|permissive|bypass>.",
		Handler: func(args []string) Result {
			if len(args) == 0 {
				return Result{Kind: ResultText, Text: "usage: /mode <strict|permissive|bypass>"}
			}
			switch args[0] {
			case "strict", "permissive", "bypass":
				return Result{Kind: ResultSetPermissionMode, PermissionMode: args[0]}
			default:
				return Result{Kind: ResultText, Text: "unknown mode: " + args[0]}
			}
		},
	})
}
