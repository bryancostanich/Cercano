package compaction

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"

	"cercano/source/server/internal/llm"
)

type taskReferenceKey struct{}

// WithTaskReference supplies relevance context, not additional material to freeze
// or authority for new approvals. It does not modify the protected task/history.
func WithTaskReference(ctx context.Context, task string) context.Context {
	return context.WithValue(ctx, taskReferenceKey{}, task)
}
func TaskReferenceFrom(ctx context.Context) string {
	task, _ := ctx.Value(taskReferenceKey{}).(string)
	return task
}

// LatestTaskReference ignores tool-result user wrappers and generated preambles.
// A main conversation may contain several tasks; do not pin its first task forever.
func LatestTaskReference(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleUser {
			continue
		}
		var text []string
		for _, b := range messages[i].Blocks {
			if b.Type == llm.BlockText && strings.TrimSpace(b.Text) != "" && !strings.HasPrefix(b.Text, "[conversation summary]") {
				text = append(text, b.Text)
			}
		}
		if len(text) > 0 {
			return strings.Join(text, "\n")
		}
	}
	return ""
}

var ErrUnhelpfulSummary = errors.New("compaction summary does not preserve useful working context")
var ErrSummarySuspended = errors.New("compaction suspended after repeated summary quality rejection")

var sourceCitation = regexp.MustCompile(`(?i)\(?source\s+sha256:[a-f0-9]+\)?`)
var statusTag = regexp.MustCompile(`\[[^\]]+\]`)
var readReceipt = regexp.MustCompile(`(?i)^(?:read|inspected|viewed|opened|searched|located|found|view|search results|grep matches)\b`)
var substantiveCode = regexp.MustCompile(`\b(?:fn|func|function|class|struct|interface|enum|def)\s+[A-Za-z_]`)

func normalizedClaim(s string) string {
	s = sourceCitation.ReplaceAllString(s, "")
	s = statusTag.ReplaceAllString(s, "")
	return strings.ToLower(strings.Trim(strings.Join(strings.Fields(s), " "), " .;:-()"))
}
func metaObjective(s string) bool {
	s = normalizedClaim(s)
	// Any "summarize <the supplied material>" objective describes this
	// compaction operation rather than the worker's task. A genuine user
	// summarization task is exempted by the caller via the task reference.
	for _, p := range []string{"summarize", "summarise", "summary of", "summarizing", "summarising"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return s == "summary prepared" || s == "summary generated" || s == "summary complete"
}
func idleClaim(s string) bool {
	s = normalizedClaim(s)
	for _, v := range []string{"awaiting further instruction", "awaiting instructions", "waiting for further instruction", "no unresolved questions", "no pending actions", "no pending work"} {
		if strings.HasPrefix(s, v) {
			return true
		}
	}
	return metaObjective(s)
}
func usefulObservation(s string) bool {
	s = normalizedClaim(s)
	if head, tail, ok := strings.Cut(s, ":"); ok && !strings.Contains(head, " ") {
		s = strings.TrimSpace(tail)
	}
	for _, v := range []string{"implementation pending", "work remains", "no code changes", "no modifications", "not modified", "summary prepared"} {
		if s == v {
			return false
		}
	}
	if s == "" || s == "none" || s == "(none)" || s == "n/a" || metaObjective(s) || idleClaim(s) {
		return false
	}
	if readReceipt.MatchString(s) {
		// Reading a range/location/hash alone is a receipt. A clause describing the
		// actual finding can still be useful, e.g. "read ...; JobKey includes epoch".
		if !strings.ContainsAny(s, ";\n") && !strings.Contains(s, " because ") && !strings.Contains(s, " shows ") && !strings.Contains(s, " contains ") && !strings.Contains(s, " includes ") {
			return false
		}
	}
	return true
}

// ValidateWorkingMemory is a conservative rejection gate for demonstrated bad
// patterns, not a semantic proof of factual correctness. It only applies when the
// span contained substantive inspected code/search evidence, so ordinary prose
// conversations and terse error-only exchanges keep their existing behavior.
func ValidateWorkingMemory(messages []llm.Message, s StructuredSummary, task string) error {
	calls := map[string]bool{}
	for _, m := range messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				switch strings.ToLower(b.ToolName) {
				case "read", "read_file", "grep", "glob", "ls", "list_directory":
					calls[b.ToolUseID] = true
				}
			}
		}
	}
	needsFindings := false
	for _, m := range messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && !b.IsError && calls[b.ToolUseRef] && len(b.Content) >= 256 && substantiveCode.MatchString(b.Content) {
				needsFindings = true
			}
		}
	}
	if !needsFindings {
		return nil
	}
	// Scope: spans that actually carried substantive inspected evidence. Prose
	// conversations and terse exchanges stay compatible with existing behavior.
	if metaObjective(s.Goal) && !metaObjective(task) {
		return ErrUnhelpfulSummary
	}
	userClosed := false
	reference := strings.ToLower(strings.TrimSpace(task))
	for _, phrase := range []string{"thanks, stop here", "wait for further instructions", "pause here", "no further work"} {
		if strings.Contains(reference, phrase) {
			userClosed = true
		}
	}
	if !userClosed {
		if idleClaim(s.State) {
			return ErrUnhelpfulSummary
		}
		for _, open := range s.OpenThreads {
			if idleClaim(open) {
				return ErrUnhelpfulSummary
			}
		}
	}
	// Findings are preferred. Substantive legacy file entries remain acceptable.
	// Decisions or a pending-work placeholder do not replace inspected code facts.
	for _, list := range [][]string{s.Findings} {
		for _, v := range list {
			if usefulObservation(v) {
				return nil
			}
		}
	}
	for _, v := range s.Files {
		if usefulObservation(v) {
			return nil
		}
	}
	return ErrUnhelpfulSummary
}

// SummaryGuard bounds rejected model attempts over its owner's lifetime. Use one
// per dispatch, or per main-conversation task. Manual regeneration gets a fresh
// budget. Successful calls do not erase earlier quality failures. The lock also
// prevents concurrent callers from exceeding the bound.
type SummaryGuard struct {
	mu              sync.Mutex
	limit, rejected int
}

func NewSummaryGuard(limit int) *SummaryGuard {
	if limit <= 0 {
		limit = 2
	}
	return &SummaryGuard{limit: limit}
}
func (g *SummaryGuard) Reset() { g.mu.Lock(); g.rejected = 0; g.mu.Unlock() }
func (g *SummaryGuard) Summarize(ctx context.Context, msgs []llm.Message, call SummarizeFunc) (StructuredSummary, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rejected >= g.limit {
		return StructuredSummary{}, errors.Join(ErrUnhelpfulSummary, ErrSummarySuspended)
	}
	s, err := call(ctx, msgs)
	if err == nil {
		err = ValidateWorkingMemory(msgs, s, TaskReferenceFrom(ctx))
	}
	if errors.Is(err, ErrUnhelpfulSummary) {
		g.rejected++
	}
	if err != nil {
		return StructuredSummary{}, err
	}
	return s, nil
}
