package compaction

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

// Fixture is one documented, realistic conversation pattern, sized for the
// bake-off. MustKeep lists substrings that compaction must not lose.
type Fixture struct {
	Name        string
	Description string
	Messages    []llm.Message
	MustKeep    []string
}

func bUser(s string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}
func bAssistant(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}
func bToolCall(id, name, input string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{
		{Type: llm.BlockToolUse, ToolUseID: id, ToolName: name, ToolInput: []byte(input)}}}
}
func bToolResult(id, content string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockToolResult, ToolUseRef: id, Content: content}}}
}

// Corpus returns the documented fixtures. Each must be pairing-valid.
func Corpus() []Fixture {
	return []Fixture{
		repeatedReadsFixture(),
		refactorManyFilesFixture(),
		lightQAFixture(),
		longDebugFixture(),
		researchFetchesFixture(),
	}
}

// repeatedReadsFixture: the dedup stressor — the same file is read 5 times as it
// is edited. Only the final read's content is needed; the earlier four are dead
// weight. MustKeep: the goal and the LATEST contents.
func repeatedReadsFixture() Fixture {
	var msgs []llm.Message
	msgs = append(msgs, bUser("Fix the off-by-one in paginate() in pager.go"))
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("r%d", i)
		msgs = append(msgs, bToolCall(id, "read", `{"path":"pager.go"}`))
		msgs = append(msgs, bToolResult(id, fmt.Sprintf("pager.go revision %d: func paginate() { /* body v%d */ }", i, i)))
	}
	msgs = append(msgs, bAssistant("Fixed the off-by-one: the loop bound now uses <= len."))
	return Fixture{
		Name:        "repeated-reads",
		Description: "Same file read 5x while editing; only the latest read matters.",
		Messages:    msgs,
		MustKeep:    []string{"off-by-one", "paginate", "revision 5"},
	}
}

// refactorManyFilesFixture: a refactor touching several distinct files, each read
// once. Nothing is superseded; dedup should reclaim little, but a summary should
// retain every file path. MustKeep: the goal + each file path.
func refactorManyFilesFixture() Fixture {
	files := []string{"auth.go", "session.go", "token.go", "middleware.go"}
	msgs := []llm.Message{bUser("Rename Session.UserID to Session.AccountID across the auth package")}
	for i, f := range files {
		id := fmt.Sprintf("f%d", i)
		msgs = append(msgs, bToolCall(id, "read", fmt.Sprintf(`{"path":%q}`, f)))
		msgs = append(msgs, bToolResult(id, fmt.Sprintf("contents of %s with UserID references", f)))
	}
	msgs = append(msgs, bAssistant("Renamed UserID to AccountID in all four files."))
	return Fixture{
		Name:        "refactor-many-files",
		Description: "Distinct files each read once; no supersession; paths must survive.",
		Messages:    msgs,
		MustKeep:    []string{"AccountID", "auth.go", "session.go", "token.go", "middleware.go"},
	}
}

// lightQAFixture: mostly prose, almost no tool use — compaction should barely
// touch it. MustKeep: the question and the decision.
func lightQAFixture() Fixture {
	return Fixture{
		Name:        "light-qa",
		Description: "Prose Q&A with minimal tool use; little to compact.",
		Messages: []llm.Message{
			bUser("Should we use a channel or a mutex for the recap timer map?"),
			bAssistant("A mutex — the map is short-lived per-conversation and contention is low."),
			bUser("Okay, go with the mutex."),
		},
		MustKeep: []string{"mutex", "recap timer"},
	}
}

// longDebugFixture: a long debugging session that revisits one hypothesis across
// many turns, reading the same file repeatedly, before the real root cause is
// found. Tests whether the goal and the FINAL root cause survive summarization.
func longDebugFixture() Fixture {
	var msgs []llm.Message
	msgs = append(msgs, bUser("Tests flake intermittently in TestPager — find why"))
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("d%d", i)
		msgs = append(msgs, bToolCall(id, "read", `{"path":"pager_test.go"}`))
		msgs = append(msgs, bToolResult(id, fmt.Sprintf("pager_test.go inspection pass %d", i)))
		msgs = append(msgs, bAssistant(fmt.Sprintf("Hypothesis %d: maybe a timing issue; not confirmed.", i)))
	}
	msgs = append(msgs, bAssistant("Root cause found: a shared map is written without a lock in paginate()."))
	return Fixture{
		Name:        "long-debug",
		Description: "Long debug session revisiting one hypothesis; final root cause must survive.",
		Messages:    msgs,
		MustKeep:    []string{"flake", "TestPager", "shared map", "without a lock"},
	}
}

// researchFetchesFixture: many distinct web fetches, each a different finding.
// Tests whether distinct facts are retained rather than blurred together.
func researchFetchesFixture() Fixture {
	findings := []struct{ url, fact string }{
		{"a.example/rram", "RRAM endurance is ~10^6 cycles"},
		{"b.example/sram", "SRAM bitcell is 6T, ~0.2 um^2 in this node"},
		{"c.example/mram", "MRAM retention exceeds 10 years at 85C"},
		{"d.example/flash", "Flash needs ~18V for erase"},
	}
	msgs := []llm.Message{bUser("Compare emerging memory technologies for the edge accelerator")}
	for i, f := range findings {
		id := fmt.Sprintf("w%d", i)
		msgs = append(msgs, bToolCall(id, "fetch", fmt.Sprintf(`{"url":%q}`, f.url)))
		msgs = append(msgs, bToolResult(id, f.fact))
	}
	msgs = append(msgs, bAssistant("Compiled the comparison across RRAM, SRAM, MRAM, and Flash."))
	return Fixture{
		Name:        "research-fetches",
		Description: "Many distinct fetches; each finding must be retained, not blurred.",
		Messages:    msgs,
		MustKeep:    []string{"RRAM", "10 years", "18V", "6T"},
	}
}
