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
