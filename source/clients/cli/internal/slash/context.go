package slash

import (
	"fmt"
	"os"
	"path/filepath"
)

// RegisterContextRegen wires /context-regen — rebuilds the current
// conversation's derived context (compaction state) from its raw turns on the
// server. The UI owns the conversation id and the streaming call; this command
// just signals intent.
func RegisterContextRegen(r *Registry) {
	r.Register(Command{
		Name: "context-regen",
		Help: "Rebuild this conversation's context from its raw turns (re-runs compaction, updates the context meter).",
		Handler: func(args []string) Result {
			return Result{Kind: ResultRegenContext}
		},
	})
}

// RegisterCompact wires /compact — digests the current compaction backlog
// incrementally, keeping existing summaries. The gentle sibling of
// /context-regen (which clears and rebuilds from scratch).
func RegisterCompact(r *Registry) {
	r.Register(Command{
		Name: "compact",
		Help: "Compact this conversation's context incrementally (digest the backlog, keep existing summaries).",
		Handler: func(args []string) Result {
			return Result{Kind: ResultCompactContext}
		},
	})
}

// RegisterClearCompactedContext wires /clear-compacted-context — drops the
// current conversation's derived compaction state (summaries, frozen boundary)
// without re-summarizing, forcing the next send-view to rehydrate from the
// full raw turn history. The recovery command when the compacted layer is bad
// (e.g. a broken summarizer froze segments behind empty summaries).
func RegisterClearCompactedContext(r *Registry) {
	r.Register(Command{
		Name: "clear-compacted-context",
		Help: "Drop this conversation's compacted summaries and rehydrate the context from its raw turns (recovery for a bad compaction; no re-summarization).",
		Handler: func(args []string) Result {
			return Result{Kind: ResultClearCompactedContext}
		},
	})
}

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
