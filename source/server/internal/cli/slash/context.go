package slash

import (
	"fmt"
	"os"
	"path/filepath"
)

// RegisterContext wires /context — shows the project context the agent
// will prepend to your turns (from .cercano/context.md under cwd). Reads
// the file directly client-side; no RPC.
func RegisterContext(r *Registry) {
	r.Register(Command{
		Name: "context",
		Help: "Show the project context (.cercano/context.md under your current cwd) the agent prepends to each turn.",
		Handler: func(args []string) Result {
			cwd, err := os.Getwd()
			if err != nil {
				return Result{Kind: ResultText, Text: "context: " + err.Error()}
			}
			path := filepath.Join(cwd, ".cercano", "context.md")
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return Result{Kind: ResultText, Text: fmt.Sprintf("no project context at %s\n(run `cercano setup` or the cercano_init MCP tool to generate one)", path)}
				}
				return Result{Kind: ResultText, Text: "context: " + err.Error()}
			}
			return Result{Kind: ResultText, Text: fmt.Sprintf("project context (%s):\n\n%s", path, string(data))}
		},
	})
}
