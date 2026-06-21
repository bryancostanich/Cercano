package slash

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cercano/source/server/internal/cli/agentclient"
)

// RegisterTools wires /tools (list) and /tool (invoke) against the client.
func RegisterTools(r *Registry, c *agentclient.Client) {
	r.Register(Command{
		Name: "tools",
		Help: "List the agent's registered tools with their permission tier and description.",
		Handler: func(args []string) Result {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			tools, err := c.ListTools(ctx)
			if err != nil {
				return Result{Kind: ResultText, Text: "tools: " + err.Error()}
			}
			if len(tools) == 0 {
				return Result{Kind: ResultText, Text: "no tools registered."}
			}
			// Render as a markdown table so the agent's Table interceptor
			// picks it up and fits it to viewport width.
			var b strings.Builder
			b.WriteString("registered tools:\n\n")
			b.WriteString("| name | perm | description |\n")
			b.WriteString("| --- | --- | --- |\n")
			for _, t := range tools {
				desc := t.Description
				if len(desc) > 200 {
					desc = desc[:200] + "…"
				}
				fmt.Fprintf(&b, "| %s | %s | %s |\n", t.Name, t.Permission, escapePipes(desc))
			}
			return Result{Kind: ResultText, Text: b.String()}
		},
	})

	r.Register(Command{
		Name: "tool",
		Help: "Invoke a tool directly: /tool <name> <json-args>. Example: /tool grep {\"pattern\":\"foo\",\"path\":\".\"}",
		Handler: func(args []string) Result {
			if len(args) == 0 {
				return Result{Kind: ResultText, Text: "usage: /tool <name> <json-args>   (e.g. /tool list_dir {\"path\":\".\"})"}
			}
			name := args[0]
			argsJSON := "{}"
			if len(args) > 1 {
				argsJSON = strings.Join(args[1:], " ")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := c.InvokeTool(ctx, name, argsJSON)
			if err != nil {
				return Result{Kind: ResultText, Text: "tool: " + err.Error()}
			}
			if res.Error != "" {
				return Result{Kind: ResultText, Text: "tool error: " + res.Error}
			}
			return Result{Kind: ResultText, Text: renderToolResult(res)}
		},
	})
}

// renderToolResult shapes a ToolResult into a string the CLI can render.
// Rows → markdown table (caught by the Table interceptor for proper layout).
// Text → fenced code block. JSON → fenced JSON block.
func renderToolResult(r *agentclient.ToolResult) string {
	var b strings.Builder
	if r.Note != "" {
		b.WriteString("_" + r.Note + "_\n\n")
	}
	switch r.Type {
	case "text":
		b.WriteString("```\n")
		b.WriteString(r.Text)
		if !strings.HasSuffix(r.Text, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```")
	case "rows":
		// Parse rows and emit a markdown table the assistant-render path
		// will then route through render.InterceptMarkdownTables → Table.
		var rows []map[string]any
		if err := json.Unmarshal([]byte(r.RowsJSON), &rows); err != nil {
			b.WriteString("(rows parse error: " + err.Error() + ")")
			return b.String()
		}
		if len(rows) == 0 {
			b.WriteString("(no rows)")
			return b.String()
		}
		// Header: stable union of keys across rows.
		keys := unionKeys(rows)
		b.WriteString("| " + strings.Join(keys, " | ") + " |\n")
		b.WriteString("| " + strings.Repeat("--- | ", len(keys)) + "\n")
		for _, row := range rows {
			cells := make([]string, len(keys))
			for i, k := range keys {
				cells[i] = escapePipes(fmt.Sprint(row[k]))
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
	case "json":
		b.WriteString("```json\n")
		b.WriteString(r.JSON)
		if !strings.HasSuffix(r.JSON, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```")
	default:
		b.WriteString("(unknown result type: " + r.Type + ")")
	}
	if r.Truncated {
		b.WriteString("\n_(truncated)_")
	}
	return b.String()
}

func unionKeys(rows []map[string]any) []string {
	seen := map[string]struct{}{}
	var order []string
	for _, row := range rows {
		for k := range row {
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			order = append(order, k)
		}
	}
	return order
}

func escapePipes(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", "\\|")
}
