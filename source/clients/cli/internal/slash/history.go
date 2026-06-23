package slash

import (
	"context"
	"strings"
	"time"

	"cercano/source/server/pkg/agentclient"
)

// CurrentConversationFn is supplied by the host (Model) so /rename knows
// which conversation to act on when no id is given.
type CurrentConversationFn func() string

// RegisterHistory wires /history (picker overlay), /resume <id>, and /rename
// against the supplied client. currentConv returns the active conversation
// id so /rename with no id-arg can target it.
func RegisterHistory(r *Registry, c *agentclient.Client, currentConv CurrentConversationFn) {
	r.Register(Command{
		Name: "history",
		Help: "Open the conversation history picker (resume an earlier session).",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenHistoryPicker}
		},
	})
	r.Register(Command{
		Name: "resume",
		Help: "Resume a conversation by id: /resume <id>. No id → opens the picker.",
		Handler: func(args []string) Result {
			if len(args) == 0 {
				return Result{Kind: ResultOpenHistoryPicker}
			}
			// Validate by hitting the agent — bad ids fail fast in the CLI
			// rather than surprising the user mid-stream.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := c.ResumeConversation(ctx, args[0]); err != nil {
				return Result{Kind: ResultText, Text: "resume failed: " + err.Error()}
			}
			return Result{Kind: ResultResumeConversation, Text: args[0]}
		},
	})

	r.Register(Command{
		Name: "rename",
		Help: "Rename the current conversation: /rename <new title>. Or rename any: /rename <id> <new title>.",
		Handler: func(args []string) Result {
			if len(args) == 0 {
				return Result{Kind: ResultText, Text: "usage: /rename <new title>   or   /rename <id> <new title>"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			// Heuristic for two-arg form: a conversation id is 24 hex chars
			// (12 bytes). If the first arg matches that shape, treat as id.
			var convID, title string
			if len(args) >= 2 && len(args[0]) == 24 && isHex(args[0]) {
				convID = args[0]
				title = strings.Join(args[1:], " ")
			} else {
				if currentConv == nil {
					return Result{Kind: ResultText, Text: "rename: no current conversation; pass <id> explicitly"}
				}
				convID = currentConv()
				title = strings.Join(args, " ")
			}
			if convID == "" {
				return Result{Kind: ResultText, Text: "rename: no current conversation"}
			}
			if err := c.RenameConversation(ctx, convID, title); err != nil {
				return Result{Kind: ResultText, Text: "rename failed: " + err.Error()}
			}
			return Result{Kind: ResultSetSessionTitle, Text: title}
		},
	})
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
