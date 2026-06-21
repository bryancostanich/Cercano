package slash

import "strings"

// RegisterBasics installs the minimal V0 commands: /help, /quit, /clear.
func RegisterBasics(r *Registry) {
	r.Register(Command{
		Name:    "quit",
		Aliases: []string{"exit"},
		Help:    "Leave the REPL.",
		Handler: func(args []string) Result { return Result{Kind: ResultQuit} },
	})
	r.Register(Command{
		Name: "clear",
		Help: "Clear the local scrollback (server-side reset deferred).",
		Handler: func(args []string) Result {
			return Result{Kind: ResultClearConversation}
		},
	})
	r.Register(Command{
		Name: "help",
		Help: "List available slash commands.",
		Handler: func(args []string) Result {
			var b strings.Builder
			b.WriteString("commands:\n")
			for _, c := range r.All() {
				b.WriteString("  /")
				b.WriteString(c.Name)
				if len(c.Aliases) > 0 {
					b.WriteString(" (")
					b.WriteString(strings.Join(c.Aliases, ", "))
					b.WriteString(")")
				}
				b.WriteString("  —  ")
				b.WriteString(c.Help)
				b.WriteString("\n")
			}
			return Result{Kind: ResultText, Text: strings.TrimRight(b.String(), "\n")}
		},
	})
}
