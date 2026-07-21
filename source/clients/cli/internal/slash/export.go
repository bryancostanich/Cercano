package slash

import "strings"

// RegisterExport wires trajectory export commands. The CLI only gathers user
// intent; the agent owns bundle construction via ExportTrajectory.
func RegisterExport(r *Registry) {
	handler := func(args []string) Result {
		if len(args) == 0 {
			return Result{Kind: ResultText, Text: "usage: /export trajectory [path]"}
		}
		if args[0] != "trajectory" && args[0] != "traj" {
			return Result{Kind: ResultText, Text: "usage: /export trajectory [path]"}
		}
		return Result{Kind: ResultOpenTrajectoryExport, Text: strings.Join(args[1:], " ")}
	}
	r.Register(Command{Name: "export", Help: "Export a conversation trajectory bundle: /export trajectory [path].", Handler: handler})
	r.Register(Command{Name: "traj", Help: "Export a conversation trajectory bundle: /traj [path].", Handler: func(args []string) Result {
		return Result{Kind: ResultOpenTrajectoryExport, Text: strings.Join(args, " ")}
	}})
}
