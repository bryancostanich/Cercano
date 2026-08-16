package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"cercano/source/server/pkg/agentclient"
)

type resumeToolBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (m *Model) hydrateTaskPaneFromResumedTurns(turns []agentclient.PersistedTurn) {
	m.taskPane.Tasks = nil
	m.taskPane.Roots = nil
	m.taskPane.ScrollY = 0
	m.taskPane.ScrollX = 0
	m.clearTaskPaneDrag()

	for _, planPath := range resumePlanPaths(turns) {
		root, ok := taskPanePlanRootFromFile(m.effectiveWorkDir(), planPath)
		if !ok {
			continue
		}
		m.applyTaskChange("updated", root)
	}
	if !m.taskPaneAvailable() {
		m.taskPane.Expanded = false
	}
}

func resumePlanPaths(turns []agentclient.PersistedTurn) []string {
	seen := map[string]bool{}
	var out []string
	for _, turn := range turns {
		if strings.TrimSpace(turn.ContentJSON) == "" {
			continue
		}
		var blocks []resumeToolBlock
		if err := json.Unmarshal([]byte(turn.ContentJSON), &blocks); err != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type != "tool_use" || block.Input == nil {
				continue
			}
			if block.Name != "request_plan_approval" && block.Name != "plan_set_status" {
				continue
			}
			var input struct {
				PlanPath string `json:"plan_path"`
			}
			if err := json.Unmarshal(block.Input, &input); err != nil {
				continue
			}
			planPath := strings.TrimSpace(input.PlanPath)
			if planPath == "" || seen[planPath] {
				continue
			}
			seen[planPath] = true
			out = append(out, planPath)
		}
	}
	return out
}

var resumeCheckboxRE = regexp.MustCompile(`^\s*- \[([ xX~!])\]\s+(.*)$`)

func taskPanePlanRootFromFile(workDir, planPath string) (*agentclient.TaskNode, bool) {
	abs := planPath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workDir, planPath)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, false
	}
	return taskPanePlanRootFromMarkdown(planPath, string(raw))
}

func taskPanePlanRootFromMarkdown(source, md string) (*agentclient.TaskNode, bool) {
	root := &agentclient.TaskNode{ID: taskPaneResumeID(source, 0), Title: strings.TrimSuffix(filepath.Base(filepath.Dir(source)), string(filepath.Separator)), Status: "pending"}
	if root.Title == "." || root.Title == "" {
		root.Title = "Plan"
	}
	var phase *agentclient.TaskNode
	var stack []*agentclient.TaskNode
	for i, line := range strings.Split(md, "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "# "):
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			if title != "" {
				root.Title = title
			}
		case strings.HasPrefix(trimmed, "## "):
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			if title == "" {
				continue
			}
			n := agentclient.TaskNode{ID: taskPaneResumeID(source, lineNo), Title: title, Status: "pending", ParentID: root.ID}
			root.Children = append(root.Children, n)
			phase = &root.Children[len(root.Children)-1]
			stack = []*agentclient.TaskNode{phase}
		default:
			match := resumeCheckboxRE.FindStringSubmatch(line)
			if match == nil || phase == nil {
				continue
			}
			indent := leadingSpaces(line)
			level := indent / 2
			if level < 0 {
				level = 0
			}
			if level+1 > len(stack) {
				level = len(stack) - 1
			}
			parent := stack[level]
			title := strings.TrimSpace(match[2])
			if title == "" {
				continue
			}
			n := agentclient.TaskNode{ID: taskPaneResumeID(source, lineNo), Title: title, Status: taskPaneStatusFromGlyph(match[1]), ParentID: parent.ID}
			parent.Children = append(parent.Children, n)
			child := &parent.Children[len(parent.Children)-1]
			stack = append(stack[:level+1], child)
		}
	}
	if len(root.Children) == 0 {
		return nil, false
	}
	return root, true
}

func taskPaneResumeID(source string, line int) string {
	return fmt.Sprintf("resume:%s:%d", source, line)
}

func taskPaneStatusFromGlyph(glyph string) string {
	switch glyph {
	case "x", "X":
		return "done"
	case "~":
		return "in_progress"
	case "!":
		return "blocked"
	default:
		return "pending"
	}
}

func leadingSpaces(s string) int {
	count := 0
	for _, r := range s {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}
