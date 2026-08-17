package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cercano/source/server/pkg/agentclient"
)

const debugTaskPaneToolName = "debug_task_pane"

var debugTaskPaneStatuses = map[string]bool{
	"pending":     true,
	"in_progress": true,
	"done":        true,
	"blocked":     true,
}

var debugTaskPaneScenarios = []string{"basic", "nested", "overflow", "wrap", "all-states"}

type debugTaskPaneToolRequest struct {
	Op       string `json:"op"`
	Action   string `json:"action"`
	Scenario string `json:"scenario"`
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Status   string `json:"status"`
	Title    string `json:"title"`
}

func (m Model) debugTaskControlsEnabled() bool {
	return m.workDirOverride != ""
}

func (m *Model) runDebugTaskPaneSlash(args []string) string {
	if len(args) == 0 || args[0] == "help" {
		if !m.debugTaskControlsEnabled() {
			return "debug commands are only available after /d"
		}
		return debugTaskPaneHelp()
	}
	if args[0] != "task" {
		return "usage: /debug help or /debug task <command>"
	}
	if !m.debugTaskControlsEnabled() {
		return "debug commands are only available after /d"
	}
	msg, err := m.runDebugTaskPaneArgs(args[1:])
	if err != nil {
		return "debug task: " + err.Error()
	}
	return msg
}

func (m *Model) runDebugTaskPaneTool(argsJSON string) (string, error) {
	if !m.debugTaskControlsEnabled() {
		return "", fmt.Errorf("debug task pane tool is only available after /d")
	}
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	var req debugTaskPaneToolRequest
	if err := json.Unmarshal([]byte(argsJSON), &req); err != nil {
		return "", fmt.Errorf("invalid JSON args: %w", err)
	}
	return m.runDebugTaskPaneRequest(req)
}

func (m *Model) runDebugTaskPaneArgs(args []string) (string, error) {
	if len(args) == 0 || args[0] == "help" {
		return debugTaskPaneHelp(), nil
	}
	switch args[0] {
	case "show", "hide", "toggle", "clear":
		if len(args) != 1 {
			return "", fmt.Errorf("usage: /debug task %s", args[0])
		}
		return m.runDebugTaskPaneRequest(debugTaskPaneToolRequest{Op: args[0]})
	case "seed":
		if len(args) != 2 {
			return "", fmt.Errorf("usage: /debug task seed <%s>", strings.Join(debugTaskPaneScenarios, "|"))
		}
		return m.runDebugTaskPaneRequest(debugTaskPaneToolRequest{Op: "seed", Scenario: args[1]})
	case "add":
		if len(args) < 4 {
			return "", fmt.Errorf("usage: /debug task add <id> <status> <title...>")
		}
		return m.runDebugTaskPaneRequest(debugTaskPaneToolRequest{Op: "add", ID: args[1], Status: args[2], Title: strings.Join(args[3:], " ")})
	case "add-child":
		if len(args) < 5 {
			return "", fmt.Errorf("usage: /debug task add-child <parent-id> <id> <status> <title...>")
		}
		return m.runDebugTaskPaneRequest(debugTaskPaneToolRequest{Op: "add-child", ParentID: args[1], ID: args[2], Status: args[3], Title: strings.Join(args[4:], " ")})
	case "status":
		if len(args) != 3 {
			return "", fmt.Errorf("usage: /debug task status <id> <pending|in_progress|done|blocked>")
		}
		return m.runDebugTaskPaneRequest(debugTaskPaneToolRequest{Op: "status", ID: args[1], Status: args[2]})
	case "title":
		if len(args) < 3 {
			return "", fmt.Errorf("usage: /debug task title <id> <title...>")
		}
		return m.runDebugTaskPaneRequest(debugTaskPaneToolRequest{Op: "title", ID: args[1], Title: strings.Join(args[2:], " ")})
	case "remove":
		if len(args) != 2 {
			return "", fmt.Errorf("usage: /debug task remove <id>")
		}
		return m.runDebugTaskPaneRequest(debugTaskPaneToolRequest{Op: "remove", ID: args[1]})
	default:
		return "", fmt.Errorf("unknown command %q; try /debug task help", args[0])
	}
}

func (m *Model) runDebugTaskPaneRequest(req debugTaskPaneToolRequest) (string, error) {
	op := strings.TrimSpace(req.Op)
	if op == "" {
		op = strings.TrimSpace(req.Action)
	}
	switch op {
	case "show":
		if !m.taskPaneAvailable() {
			return "", fmt.Errorf("task pane has no tasks to show; run /debug task seed basic first")
		}
		m.taskPane.Expanded = true
		m.clearTaskPaneDrag()
		m.relayout()
		return "debug task pane shown", nil
	case "hide":
		m.taskPane.Expanded = false
		m.clearTaskPaneDrag()
		m.relayout()
		return "debug task pane hidden", nil
	case "toggle":
		if !m.taskPaneAvailable() {
			return "", fmt.Errorf("task pane has no tasks to toggle; run /debug task seed basic first")
		}
		m.toggleTaskPane()
		if m.taskPane.Expanded {
			return "debug task pane shown", nil
		}
		return "debug task pane hidden", nil
	case "clear":
		m.taskPane.Tasks = nil
		m.taskPane.Roots = nil
		m.taskPane.ScrollX = 0
		m.taskPane.ScrollY = 0
		m.taskPane.Expanded = false
		m.clearTaskPaneDrag()
		m.relayout()
		return "debug task pane cleared", nil
	case "seed":
		return m.seedDebugTaskPaneScenario(req.Scenario)
	case "add":
		return m.addDebugTask(req.ID, "", req.Status, req.Title)
	case "add-child":
		return m.addDebugTask(req.ID, req.ParentID, req.Status, req.Title)
	case "status":
		return m.setDebugTaskStatus(req.ID, req.Status)
	case "title":
		return m.setDebugTaskTitle(req.ID, req.Title)
	case "remove":
		return m.removeDebugTask(req.ID)
	default:
		return "", fmt.Errorf("unknown op %q; valid ops: show, hide, toggle, clear, seed, add, add-child, status, title, remove", op)
	}
}

func (m *Model) seedDebugTaskPaneScenario(scenario string) (string, error) {
	scenario = strings.TrimSpace(scenario)
	m.taskPane.Tasks = nil
	m.taskPane.Roots = nil
	m.taskPane.ScrollX = 0
	m.taskPane.ScrollY = 0
	m.clearTaskPaneDrag()

	switch scenario {
	case "basic":
		m.applyTaskChange("added", debugTaskNode("debug:basic:1", "pending", "Basic debug task"))
	case "nested":
		m.applyTaskChange("added", &agentclient.TaskNode{ID: "debug:nested:phase", Title: "Debug nested phase", Status: "pending", Children: []agentclient.TaskNode{
			{ID: "debug:nested:1", ParentID: "debug:nested:phase", Title: "Parent task", Status: "in_progress", Children: []agentclient.TaskNode{
				{ID: "debug:nested:1a", ParentID: "debug:nested:1", Title: "Finished child", Status: "done"},
				{ID: "debug:nested:1b", ParentID: "debug:nested:1", Title: "Pending child", Status: "pending"},
			}},
		}})
	case "overflow":
		for i := 1; i <= 40; i++ {
			m.applyTaskChange("added", debugTaskNode(fmt.Sprintf("debug:overflow:%02d", i), "pending", fmt.Sprintf("Overflow task %02d", i)))
		}
	case "wrap":
		m.applyTaskChange("added", debugTaskNode("debug:wrap:1", "pending", "This debug task has a very long title that should wrap across multiple task pane lines while preserving indentation"))
		m.applyTaskChange("added", &agentclient.TaskNode{ID: "debug:wrap:phase", Title: "Wrapped nested phase", Status: "pending", Children: []agentclient.TaskNode{
			{ID: "debug:wrap:child", ParentID: "debug:wrap:phase", Title: "Nested child title that also wraps and should align underneath the task text instead of the glyph", Status: "pending"},
		}})
	case "all-states":
		m.applyTaskChange("added", debugTaskNode("debug:state:pending", "pending", "Pending task"))
		m.applyTaskChange("added", debugTaskNode("debug:state:active", "in_progress", "In-progress task"))
		m.applyTaskChange("added", debugTaskNode("debug:state:done", "done", "Done task"))
		m.applyTaskChange("added", debugTaskNode("debug:state:blocked", "blocked", "Blocked task"))
	default:
		return "", fmt.Errorf("unknown scenario %q; valid scenarios: %s", scenario, strings.Join(debugTaskPaneScenarios, ", "))
	}
	m.taskPane.Expanded = true
	m.relayout()
	return "debug task pane seeded: " + scenario, nil
}

func (m *Model) addDebugTask(id, parentID, status, title string) (string, error) {
	id = strings.TrimSpace(id)
	parentID = strings.TrimSpace(parentID)
	status = strings.TrimSpace(status)
	title = strings.TrimSpace(title)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if !debugTaskPaneStatuses[status] {
		return "", fmt.Errorf("invalid status %q; valid statuses: pending, in_progress, done, blocked", status)
	}
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	if m.taskPane.Tasks != nil {
		if _, exists := m.taskPane.Tasks[id]; exists {
			return "", fmt.Errorf("task %q already exists", id)
		}
	}
	if parentID != "" {
		if m.taskPane.Tasks == nil || m.taskPane.Tasks[parentID].ID == "" {
			return "", fmt.Errorf("parent task %q does not exist", parentID)
		}
	}
	m.applyTaskChange("added", &agentclient.TaskNode{ID: id, ParentID: parentID, Status: status, Title: title})
	return "debug task added: " + id, nil
}

func (m *Model) setDebugTaskStatus(id, status string) (string, error) {
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	if !debugTaskPaneStatuses[status] {
		return "", fmt.Errorf("invalid status %q; valid statuses: pending, in_progress, done, blocked", status)
	}
	task, ok := m.taskPane.Tasks[id]
	if !ok {
		return "", fmt.Errorf("task %q does not exist", id)
	}
	task.Status = status
	m.taskPane.Tasks[id] = task
	m.relayout()
	return "debug task status updated: " + id, nil
}

func (m *Model) setDebugTaskTitle(id, title string) (string, error) {
	id = strings.TrimSpace(id)
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	task, ok := m.taskPane.Tasks[id]
	if !ok {
		return "", fmt.Errorf("task %q does not exist", id)
	}
	task.Title = title
	m.taskPane.Tasks[id] = task
	m.relayout()
	return "debug task title updated: " + id, nil
}

func (m *Model) removeDebugTask(id string) (string, error) {
	id = strings.TrimSpace(id)
	task, ok := m.taskPane.Tasks[id]
	if !ok {
		return "", fmt.Errorf("task %q does not exist", id)
	}
	m.applyTaskChange("removed", m.debugTaskPaneRemovalNode(task))
	return "debug task removed: " + id, nil
}

func (m *Model) debugTaskPaneRemovalNode(task taskPaneTask) *agentclient.TaskNode {
	children := make([]agentclient.TaskNode, 0, len(task.Children))
	for _, childID := range task.Children {
		child, ok := m.taskPane.Tasks[childID]
		if !ok {
			continue
		}
		children = append(children, *m.debugTaskPaneRemovalNode(child))
	}
	return &agentclient.TaskNode{ID: task.ID, ParentID: task.ParentID, Children: children}
}

func debugTaskNode(id, status, title string) *agentclient.TaskNode {
	return &agentclient.TaskNode{ID: id, Status: status, Title: title}
}

func debugTaskPaneHelp() string {
	var b strings.Builder
	b.WriteString("debug task pane controls (dev mode only):\n")
	b.WriteString("  /debug task show|hide|toggle|clear\n")
	b.WriteString("  /debug task seed <basic|nested|overflow|wrap|all-states>\n")
	b.WriteString("  /debug task add <id> <pending|in_progress|done|blocked> <title...>\n")
	b.WriteString("  /debug task add-child <parent-id> <id> <pending|in_progress|done|blocked> <title...>\n")
	b.WriteString("  /debug task status <id> <pending|in_progress|done|blocked>\n")
	b.WriteString("  /debug task title <id> <title...>\n")
	b.WriteString("  /debug task remove <id>\n")
	b.WriteString("agent-callable local bridge: /tool debug_task_pane {\"op\":\"seed\",\"scenario\":\"overflow\"}\n")
	return b.String()
}

func debugTaskPaneScenarioList() []string {
	out := append([]string(nil), debugTaskPaneScenarios...)
	sort.Strings(out)
	return out
}
