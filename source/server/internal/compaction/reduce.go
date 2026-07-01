package compaction

// Reduce reconciles segment summaries into one via the deterministic union
// MergeSummaries. Previously ran an LLM re-summarize pass for len(parts) > 1;
// that path fabricated content that wasn't in the inputs — summaries had
// already been reduced to structured form, so a second model pass could only
// paraphrase or invent. MergeSummaries is lossless: it unions Decisions and
// OpenThreads with dedup (order preserved), unions Files with recent
// overriding older, keeps the first non-empty Goal and last non-empty State.
func Reduce(parts []StructuredSummary) StructuredSummary {
	return MergeSummaries(parts)
}
