package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

const enableToolsName = "enable_tools"

var enableToolsSchema = json.RawMessage(`{"type":"object","properties":{"tools":{"type":"array","items":{"type":"string"},"description":"Tool names to enable for later calls in this turn."}},"required":["tools"],"additionalProperties":false}`)

var compactFallbackTools = map[string]bool{
	// Read/search/inspection.
	"Read": true, "Grep": true, "Glob": true, "LS": true, "ViewImage": true,
	"stat_file": true, "inspect_image": true, "fetch": true,
	// Focused local code changes and checks. Existing permission/profile gates
	// still decide whether W/X tools may actually execute.
	"Edit": true, "Write": true, "Bash": true, "rm_file": true,
	// Git inspection and local checkpointing used by Cercano's workflow. Push,
	// reset, recover, land, and other branch-mutating operations stay hidden in
	// compact fallback unless explicitly hydrated later.
	"git_info": true, "git_status": true, "git_diff_stat": true, "git_log": true,
	"git_add": true, "git_commit": true, "checkpoint": true,
	// Native agent workflow and local model helpers.
	"dispatch": true, "workflow": true, "local": true, "classify": true,
	"explain": true, "extract": true, "summarize": true, "review": true,
	"get_protocol": true,
	// Planning/autonomous handoff and status tools that the active profile may
	// require even in a narrowed catalog.
	"suggest_plan": true, "request_plan_approval": true, "plan_exit": true,
	"plan_set_status": true, "suggest_autonomous": true, "capture_decision": true,
	"request_autonomous_exit": true, "complete_autonomous_review": true, "auto_exit": true,
}

func compactFallbackAllows(_ llm.Permission, name string) bool {
	return name == enableToolsName || compactFallbackTools[name]
}

func combineAllows(a, b func(llm.Permission, string) bool) func(llm.Permission, string) bool {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(tier llm.Permission, name string) bool {
		return a(tier, name) && b(tier, name)
	}
}

func enableToolsCatalogEntry() llm.Tool {
	return llm.Tool{
		Name:        enableToolsName,
		Description: "Enable full schemas for named tools that are listed in the compact tool directory and allowed by the active permission profile.",
		Schema:      enableToolsSchema,
		Permission:  llm.PermR,
	}
}

func buildCompactToolCatalog(reg *agenttools.Registry, profile Profile, tight bool, hydrated map[string]bool) []llm.Tool {
	var allowTool func(llm.Permission, string) bool
	if profile.Restricts() {
		allowTool = profile.Allows
	}
	if tight {
		allowTool = combineAllows(allowTool, func(tier llm.Permission, name string) bool {
			return compactFallbackAllows(tier, name) || hydrated[name]
		})
	}
	catalog := agenttools.BuildToolCatalogFiltered(reg, allowTool)
	if tight {
		catalog = append(catalog, enableToolsCatalogEntry())
		sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	}
	return catalog
}

func compactToolDirectory(reg *agenttools.Registry, profile Profile, hydrated map[string]bool) string {
	entries := []string{}
	for _, tool := range reg.All() {
		tier := agenttools.PermissionToLLM(tool.Permission())
		if !profile.Allows(tier, tool.Name()) {
			continue
		}
		if compactFallbackAllows(tier, tool.Name()) || hydrated[tool.Name()] {
			continue
		}
		desc := strings.TrimSpace(tool.Description())
		if desc == "" {
			desc = "no description"
		}
		if i := strings.Index(desc, "\n"); i >= 0 {
			desc = desc[:i]
		}
		entries = append(entries, fmt.Sprintf("- %s [%s]: %s", tool.Name(), tier, truncateRunes(desc, 120)))
	}
	if len(entries) == 0 {
		return ""
	}
	sort.Strings(entries)
	return "\n\nCOMPACT TOOL DIRECTORY: only a reduced tool catalog is loaded to fit the smaller local context. If you need another listed tool, call enable_tools with its name; its full schema will be available on the next model turn.\n" + strings.Join(entries, "\n")
}

type enableToolsArgs struct {
	Tools []string `json:"tools"`
}

func handleEnableTools(input json.RawMessage, reg *agenttools.Registry, profile Profile, hydrated map[string]bool) (string, bool) {
	var args enableToolsArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return "enable_tools arguments must be valid JSON with a tools array", true
	}
	if len(args.Tools) == 0 {
		return "enable_tools: no tools requested", false
	}
	enabled := []string{}
	denied := []string{}
	unknown := []string{}
	for _, name := range args.Tools {
		tool, ok := reg.Get(name)
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		tier := agenttools.PermissionToLLM(tool.Permission())
		if !profile.Allows(tier, name) {
			denied = append(denied, name)
			continue
		}
		hydrated[name] = true
		enabled = append(enabled, name)
	}
	parts := []string{}
	if len(enabled) > 0 {
		sort.Strings(enabled)
		parts = append(parts, "enabled: "+strings.Join(enabled, ", "))
	}
	if len(denied) > 0 {
		sort.Strings(denied)
		parts = append(parts, "denied by active profile: "+strings.Join(denied, ", "))
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		parts = append(parts, "unknown: "+strings.Join(unknown, ", "))
	}
	if len(parts) == 0 {
		return "enable_tools: no changes", false
	}
	return "enable_tools: " + strings.Join(parts, "; "), len(denied) > 0 || len(unknown) > 0
}
