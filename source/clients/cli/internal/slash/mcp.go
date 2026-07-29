package slash

import (
	"context"
	"time"

	"cercano/source/server/pkg/agentclient"
)

// parseMcp splits /mcp arguments into a subcommand and its remaining args.
// Bare `/mcp` means `list`.
func parseMcp(args []string) (string, []string) {
	if len(args) == 0 {
		return "list", nil
	}
	return args[0], args[1:]
}

// RegisterMcp wires /mcp list|add|remove|restart against the client.
func RegisterMcp(r *Registry, c *agentclient.Client) {
	r.Register(Command{
		Name: "mcp",
		Help: "Manage hosted MCP servers: /mcp [list] | add <name> <cmd> [args…] | remove <name> | restart <name>",
		Handler: func(args []string) Result {
			sub, rest := parseMcp(args)
			switch sub {
			case "list":
				// Bare /mcp (and /mcp list) opens the MCP config tab — a live,
				// managed view — rather than dumping a one-shot table into
				// scrollback. add/remove/restart stay as CLI fast paths below.
				return Result{Kind: ResultOpenMcpConfig}

			case "add":
				if len(rest) < 2 {
					return Result{Kind: ResultText, Text: "usage: /mcp add <name> <command> [args…]"}
				}
				name := rest[0]
				command := rest[1]
				cmdArgs := rest[2:]
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := c.AddMcpServer(ctx, name, command, cmdArgs, nil); err != nil {
					return Result{Kind: ResultText, Text: "mcp add: " + err.Error()}
				}
				return Result{Kind: ResultText, Text: "added " + name}

			case "remove":
				if len(rest) < 1 {
					return Result{Kind: ResultText, Text: "usage: /mcp remove <name>"}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := c.RemoveMcpServer(ctx, rest[0]); err != nil {
					return Result{Kind: ResultText, Text: "mcp remove: " + err.Error()}
				}
				return Result{Kind: ResultText, Text: "removed " + rest[0]}

			case "restart":
				if len(rest) < 1 {
					return Result{Kind: ResultText, Text: "usage: /mcp restart <name>"}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := c.RestartMcpServer(ctx, rest[0]); err != nil {
					return Result{Kind: ResultText, Text: "mcp restart: " + err.Error()}
				}
				return Result{Kind: ResultText, Text: "restarted " + rest[0]}

			default:
				return Result{Kind: ResultText, Text: "usage: /mcp [list] | add <name> <cmd> [args…] | remove <name> | restart <name>"}
			}
		},
	})
}
