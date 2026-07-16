package compaction

import "cercano/source/server/internal/llm"

// AssembleSendView builds the final message array: a single summary preamble
// (omitted when the summary is empty), then the body, repaired so tool-use
// pairing is always valid.
func AssembleSendView(summary StructuredSummary, body []llm.Message) []llm.Message {
	var view []llm.Message
	if !summary.IsEmpty() {
		view = append(view, llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{summary.RenderBlock()}})
	}
	view = append(view, body...)
	return llm.RepairPairing(view)
}

// IsEmpty reports whether the summary carries no content at all. Exported so
// callers of a SummarizeFunc can distinguish "summarized to nothing" (a
// summarizer failure — empty model output, unparseable format) from a real
// summary before persisting anything derived from it.
func (s StructuredSummary) IsEmpty() bool {
	return s.Goal == "" && s.State == "" &&
		len(s.Decisions) == 0 && len(s.Proposals) == 0 &&
		len(s.OpenThreads) == 0 && len(s.Files) == 0
}
