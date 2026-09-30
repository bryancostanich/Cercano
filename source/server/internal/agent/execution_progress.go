package agent

import (
	"cercano/source/server/internal/llm"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// This ledger is independent of lossy model history. It records observations,
// never inferred completion, and retains neither tool-result text nor secrets
// from file bodies. Instances belong to one loop, not a provider or worker.
type executionProgress struct {
	completed, failed                int
	recentActions, recentInspections []string
	detect                           bool
	observations                     map[[32]byte][32]byte
	repeated                         int
	warned                           bool
}

func inspectionTool(name string) bool {
	switch name {
	case "Read", "read_file", "Grep", "grep", "search", "Glob", "glob", "LS", "list_dir", "StatFile", "stat_file", "git_status", "git_info", "git_log", "git_diff_stat":
		return true
	}
	return false
}
func boundedEvidence(s string) string {
	r := []rune(s)
	if len(r) > 240 {
		s = string(r[:240]) + "…"
	}
	return fmt.Sprintf("%q", s)
}
func actionSubject(call llm.Block) string {
	label := truncateRunes(call.ToolName, 80)
	var args map[string]json.RawMessage
	_ = json.Unmarshal(call.ToolInput, &args)
	var path string
	_ = json.Unmarshal(args["path"], &path)
	if path != "" {
		return label + " " + boundedEvidence(path)
	}
	if call.ToolName == "RunCommand" || call.ToolName == "Bash" || call.ToolName == "run_command" {
		var cmd []string
		_ = json.Unmarshal(args["cmd"], &cmd)
		return label + " " + boundedEvidence(strings.Join(cmd, " "))
	}
	return label
}
func appendRecent(items []string, item string, limit int) []string {
	items = append(items, item)
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items
}

func (p *executionProgress) observe(call, result llm.Block) {
	p.completed++
	status := "reported success"
	if result.IsError {
		p.failed++
		status = "reported error"
	}
	description := actionSubject(call) + " — " + status
	inspection := inspectionTool(call.ToolName)
	if inspection {
		p.recentInspections = appendRecent(p.recentInspections, description, 4)
	} else {
		p.recentActions = appendRecent(p.recentActions, description, 8)
	}
	if !p.detect {
		return
	}
	if !inspection {
		// A command or unknown tool may change the files/environment. Do not call
		// later rereads pointless merely because they resemble pre-command reads.
		p.observations = nil
		p.repeated = 0
		p.warned = false
		return
	}
	if p.observations == nil {
		p.observations = map[[32]byte][32]byte{}
	}
	// Canonical JSON removes formatting/key-order differences, not semantic
	// distinctions such as paths or ranges. UseNumber preserves large numbers.
	var args any
	dec := json.NewDecoder(strings.NewReader(string(call.ToolInput)))
	dec.UseNumber()
	normalized := call.ToolInput
	if dec.Decode(&args) == nil {
		if b, e := json.Marshal(args); e == nil {
			normalized = b
		}
	}
	key := sha256.Sum256(append([]byte(call.ToolName+"\x00"), normalized...))
	value := sha256.Sum256([]byte(fmt.Sprintf("%t\x00%s", result.IsError, result.Content)))
	if previous, ok := p.observations[key]; ok && previous == value {
		p.repeated++
	} else {
		p.repeated = 0
		p.warned = false
	}
	if len(p.observations) >= 256 {
		p.observations = map[[32]byte][32]byte{}
	}
	p.observations[key] = value
}

// Six consecutive repeated observations trigger a one-shot steering notice,
// not an automatic failure. Novel evidence or a potential mutation resets it.
// Absence of edits alone is NOT a signal (reconnaissance is legitimate work).
func (p *executionProgress) notice() string {
	if !p.detect || p.repeated < 6 || p.warned {
		return ""
	}
	p.warned = true
	return "[dispatch progress check] Six consecutive inspection calls repeated previously observed, unchanged results. Review the evidence already collected and advance the delegated task, or explain the specific missing information/blocker. Do not claim completion without verification."
}

func (p *executionProgress) handoff(pending []llm.Block) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Partial work — task NOT completed. %d completed tool calls (%d reported errors).\n", p.completed, p.failed)
	if len(p.recentActions) == 0 {
		b.WriteString("No non-inspection actions executed in this loop.\n")
	} else {
		b.WriteString("Recent executed actions (tool status, not proof of overall completion):\n")
		for _, a := range p.recentActions {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	if len(p.recentInspections) > 0 {
		b.WriteString("Recent inspections:\n")
		for _, a := range p.recentInspections {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	if len(pending) > 0 {
		names := []string{}
		for i, call := range pending {
			if i == 8 {
				names = append(names, "…")
				break
			}
			names = append(names, truncateRunes(call.ToolName, 80))
		}
		fmt.Fprintf(&b, "Requested but not executed at the stop: %s.\n", strings.Join(names, ", "))
	}
	b.WriteString("Review the recorded changes and verification results before continuing. This handoff does not claim the task is finished.")
	return b.String()
}
