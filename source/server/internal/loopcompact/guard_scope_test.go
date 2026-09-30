package loopcompact

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/llm"
)

// codeHistory exceeds the activation floor and carries substantive inspected
// code, so the working-memory gate applies to the resulting summary.
func codeHistory() []llm.Message {
	out := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Implement grading-only aperture reuse"}}}}
	for i := 0; i < 12; i++ {
		out = append(out,
			llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "r", ToolName: "Read", ToolInput: []byte(`{"path":"job.rs"}`)}}},
			llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "r", Content: strings.Repeat("pub struct SiteMeshJobKey { grading: GradingSnapshot, terrain_epoch: TerrainEpoch }\npub fn build_site_mesh_for_key_with_stats_cancellable() {}\n", 40)}}},
		)
	}
	return out
}

func receiptSummarizer(calls *int64, mu *sync.Mutex) compaction.SummarizeFunc {
	return func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		mu.Lock()
		*calls++
		mu.Unlock()
		// The exact shape recorded in dispatch a4bfafd8578002fc6b9ab73c.
		return compaction.ParseSummary("GOAL: Summarize the conversation span for later reference\nFILES:\n- job.rs: [verified] source sha256:8fb51aa3\nSTATE: Summary prepared."), nil
	}
}

// testFactory mirrors NewFactory's per-dispatch construction (Options is the
// same seam the factory uses) while injecting a scripted summarizer.
func testFactory(summarize compaction.SummarizeFunc) func() agent.LoopCompactor {
	cfg := compactor.DefaultConfig()
	cfg.ActivationFloorTokens = 100
	cfg.SegmentTokens = 4000
	cfg.VerbatimRecent = 2
	return func() agent.LoopCompactor {
		return New(Options{Config: cfg, Summarize: summarize})
	}
}

// The in-loop (sub-agent) path must pass the task reference through to its
// summarizer, exactly like the store-backed main-thread generator. Compaction
// runs before the model request, so this drives the compactor directly with the
// same context the tool loop stamps.
func TestDispatchCompactionSuppliesTaskReference(t *testing.T) {
	task := "Implement grading-only aperture reuse; preserve cancellation"
	seen := make(chan string, 4)
	factory := testFactory(func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		seen <- compaction.TaskReferenceFrom(ctx)
		return compaction.StructuredSummary{Goal: "Implement aperture reuse", Findings: []string{"job.rs: SiteMeshJobKey carries grading and terrain_epoch; non-grading fields must invalidate reuse."}, State: "Implementation pending"}, nil
	})
	hist := codeHistory()
	ctx := compaction.WithTaskReference(t.Context(), task)
	out, _, err := factory().CompactLoopHistory(ctx, hist)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) >= len(hist) {
		t.Fatalf("accepted summary did not reduce history: %d vs %d", len(out), len(hist))
	}
	select {
	case got := <-seen:
		if got != task {
			t.Fatalf("sub-agent summarizer task reference = %q, want %q", got, task)
		}
	default:
		t.Fatal("summarizer never ran; cannot claim the sub-agent path was covered")
	}
}

// The in-loop compactor belongs to dispatches. Main chat compaction is
// background-only (compactiongen), so the main turn path must not wire one up:
// that is what keeps conversational input ("push", "continue") from ever being
// presented to a summarizer as an assigned task.
func TestOnlyDispatchWiresTheInLoopCompactor(t *testing.T) {
	root := "../.."
	mainTurn, err := os.ReadFile(filepath.Join(root, "internal/runner/core.go"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(mainTurn, []byte("LoopCompactor:")) {
		t.Fatal("main turn path now wires an in-loop compactor; its UserInput is conversational, not an assigned task")
	}

	var setters []string
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(src, []byte("LoopCompactor:")) {
			setters = append(setters, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(setters) != 1 || !strings.HasSuffix(setters[0], "internal/hostsvc/tools/tools.go") {
		t.Fatalf("in-loop compactor wired outside the dispatch path: %v", setters)
	}
}
