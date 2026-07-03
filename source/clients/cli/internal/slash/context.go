package slash

import (
	"fmt"
	"os"
	"path/filepath"
)

// RegisterContext wires /context — shows the project context the agent
// will prepend to your turns (from .cercano/context.md under the effective
// work dir). Reads the file directly client-side; no RPC. workDir is called
// to obtain the directory; a nil getter falls back to os.Getwd().
func RegisterContext(r *Registry, workDir func() string) {
	r.Register(Command{
		Name: "context",
		Help: "Show the project context (.cercano/context.md under your current cwd) the agent prepends to each turn.",
		Handler: func(args []string) Result {
			var cwd string
			if workDir != nil {
				cwd = workDir()
			} else {
				var err error
				cwd, err = os.Getwd()
				if err != nil {
					return Result{Kind: ResultText, Text: "context: " + err.Error()}
				}
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
