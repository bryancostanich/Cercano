package compaction

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func budgetTextMsg(role llm.Role, text string) llm.Message {
	return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
}

func TestEstimateSummaryBudget_OutputReserveCanOverflow(t *testing.T) {
	// Sized against the real tokenizer rather than a fixed guess: this fixture
	// used 1000 NUL bytes annotated "about 251 tokens", but NUL tokenizes 1:1,
	// so the assumption only held under the old characters/4 heuristic.
	prompt := strings.Repeat("summary fidelity rules apply to every section ", 60)
	promptTokens := contextmeter.Default().Count(prompt)
	// Window chosen so the usable budget sits exactly 200 tokens above the
	// prompt: a 100-token reserve fits, a 300-token reserve cannot.
	window := int(float64(promptTokens+200)/summaryBudgetSafetyFraction) + 1
	fit := EstimateSummaryBudget(prompt, 100, window)
	if !fit.Fits {
		t.Fatalf("expected smaller reserve to fit: %+v", fit)
	}
	over := EstimateSummaryBudget(prompt, 300, window)
	if over.Fits {
		t.Fatalf("expected reserve to overflow budget: %+v", over)
	}
}

func TestPackSummaryChunks_OneFittingChunk(t *testing.T) {
	msgs := []llm.Message{budgetTextMsg(llm.RoleUser, "one"), budgetTextMsg(llm.RoleAssistant, "two")}
	chunks, err := PackSummaryChunks(msgs, 16000, DefaultSummaryOutputReserve)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || len(chunks[0]) != 2 {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestPackSummaryChunks_MultipleChunksStableOrder(t *testing.T) {
	msgs := []llm.Message{}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, budgetTextMsg(llm.RoleUser, fmt.Sprintf("msg-%d %s", i, string(make([]byte, 1800)))))
	}
	chunks, err := PackSummaryChunks(msgs, 3000, 256)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	var got []string
	for _, chunk := range chunks {
		budget := EstimateSummaryBudget(BuildSummaryPrompt(chunk), 256, 3000)
		if !budget.Fits {
			t.Fatalf("chunk over budget: %+v", budget)
		}
		for _, msg := range chunk {
			got = append(got, msg.Blocks[0].Text[:5])
		}
	}
	want := []string{"msg-0", "msg-1", "msg-2", "msg-3", "msg-4"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order changed: got %v want %v", got, want)
		}
	}
}

func TestPackSummaryChunks_SingleOversizedTextMessageSplitsLosslessly(t *testing.T) {
	text := string(make([]byte, 20000))
	msgs := []llm.Message{budgetTextMsg(llm.RoleUser, text)}
	chunks, err := PackSummaryChunks(msgs, 2000, 256)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected oversized message to split into multiple chunks, got %d", len(chunks))
	}
	var got string
	for _, chunk := range chunks {
		if len(chunk) != 1 {
			t.Fatalf("split oversized chunks should contain one synthetic message, got %#v", chunk)
		}
		budget := EstimateSummaryBudget(BuildSummaryPrompt(chunk), 256, 2000)
		if !budget.Fits {
			t.Fatalf("split chunk over budget: %+v", budget)
		}
		got += chunk[0].Blocks[0].Text
	}
	if got != text {
		t.Fatalf("split text did not round trip: got %d bytes want %d", len(got), len(text))
	}
}

func TestPackSummaryChunks_SingleOversizedUnsplittableBlockDefers(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolName: "huge", ToolInput: []byte(fmt.Sprintf(`{"payload":%q}`, string(make([]byte, 20000))))}}}}
	_, err := PackSummaryChunks(msgs, 2000, 256)
	var def *DeferralError
	if !errors.As(err, &def) {
		t.Fatalf("expected DeferralError, got %T %v", err, err)
	}
}

// The budget estimator previously used len([]rune(s))/4 + 1, which undercounts
// dense content. These tests pin the real-tokenizer behavior: the estimate must
// track an actual tokenizer, and must NOT be reproducible by the old heuristic
// on content types compaction actually sees (JSON tool input, code, diffs).
func TestEstimateSummaryBudgetUsesRealTokenizer(t *testing.T) {
	tok := contextmeter.Default()
	cases := map[string]string{
		"prose": strings.Repeat("The compaction pass walks the frozen boundary forward. ", 40),
		"json":  strings.Repeat(`{"path":"/src/a.go","old":"x","new":"y","n":12345}`, 40),
		"code":  strings.Repeat("func (g *Generator) Advance(ctx context.Context) error {\n\treturn nil\n}\n", 40),
		"diff":  strings.Repeat("-\tif x != nil {\n+\tif x == nil || y != 0 {\n", 40),
	}
	for name, s := range cases {
		got := EstimateSummaryBudget(s, 100, 100000).PromptTokens
		if want := tok.Count(s); got != want {
			t.Errorf("%s: PromptTokens = %d, want real tokenizer count %d", name, got, want)
		}
	}
}

func TestEstimateSummaryBudgetDoesNotUndercountDenseContent(t *testing.T) {
	// Regression guard for the specific defect: the old heuristic reported far
	// fewer tokens than reality for JSON/code, so "fits" was a lie.
	for name, s := range map[string]string{
		"json":  strings.Repeat(`{"path":"/src/a.go","old":"x","new":"y","n":12345}`, 40),
		"diff":  strings.Repeat("-\tif x != nil {\n+\tif x == nil || y != 0 {\n", 40),
		"shell": strings.Repeat("drwxr-xr-x  12 u  staff   384 Sep  2 21:14 internal/\n", 40),
	} {
		old := len([]rune(s))/4 + 1
		got := EstimateSummaryBudget(s, 100, 100000).PromptTokens
		if got <= old {
			t.Errorf("%s: estimate %d did not exceed old heuristic %d; dense content is still undercounted", name, got, old)
		}
	}
}

// A chunk that the packer says fits must actually fit when measured with the
// real tokenizer. Under the old heuristic this invariant did not hold.
func TestPackSummaryChunksProducesTrulyFittingChunks(t *testing.T) {
	tok := contextmeter.Default()
	const window, reserve = 3000, 256
	var msgs []llm.Message
	for i := 0; i < 12; i++ {
		msgs = append(msgs, budgetTextMsg(llm.RoleUser, strings.Repeat(`{"tool":"Edit","path":"/src/x.go","n":9876}`, 30)))
	}
	chunks, err := PackSummaryChunks(msgs, window, reserve)
	if err != nil {
		t.Fatalf("PackSummaryChunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks produced")
	}
	limit := int(float64(window) * summaryBudgetSafetyFraction)
	for i, c := range chunks {
		if real := tok.Count(BuildSummaryPrompt(c)) + reserve; real > limit {
			t.Errorf("chunk %d: real cost %d exceeds budget %d", i, real, limit)
		}
	}
}
