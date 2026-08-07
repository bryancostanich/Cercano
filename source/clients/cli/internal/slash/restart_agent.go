package slash

import "strings"

// RegisterRestartAgent wires /restart-agent (alias /bounce) — restart the
// singleton agent process. The agent drains in-flight turns, stops its runtime
// (llama-server) children, and exits; the CLI's reconnect loop then auto-launches
// a fresh agent. Useful after rebuilding the agent binary or to clear
// accumulated runtime state. Any trailing text is passed through as the reason.
func RegisterRestartAgent(r *Registry) {
	r.Register(Command{
		Name:    "restart-agent",
		Aliases: []string{"bounce"},
		Help:    "Restart the agent process (drains turns, stops runtime children, reconnects). Usage: /restart-agent [reason]",
		Handler: func(args []string) Result {
			reason := strings.TrimSpace(strings.Join(args, " "))
			if reason == "" {
				reason = "user-requested restart"
			}
			return Result{Kind: ResultRestartAgent, Text: reason}
		},
	})
}
