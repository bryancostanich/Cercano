package dispatchtrace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cercano/source/server/internal/llm"
)

func setup(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "trace")
	t.Setenv(EnableEnv, "1")
	t.Setenv("CERCANO_DISPATCH_TRACE_PARENT", "parent")
	t.Setenv(DirEnv, dir)
	return dir
}

func TestScopedSingleDispatch(t *testing.T) {
	dir := setup(t)
	if tr := Begin("other", "wrong-parent"); tr != nil {
		tr.Close()
		t.Fatal("captured unrelated dispatch")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("unrelated dispatch created artifacts")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr := Begin("child", "parent")
			if tr != nil {
				mu.Lock()
				count++
				mu.Unlock()
				tr.Close()
			}
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatalf("captured %d dispatches, want exactly one", count)
	}
	if tr := Begin("later", "parent"); tr != nil {
		tr.Close()
		t.Fatal("claim did not persist after close")
	}
}

func TestDisabledCreatesNothing(t *testing.T) {
	dir := setup(t)
	t.Setenv(EnableEnv, "")
	tr := Begin("child", "parent")
	if tr != nil {
		t.Fatal("enabled without opt-in")
	}
	tr.ModelRequest(1, "provider", llm.ChatRequest{}, BudgetView{})
	tr.ModelResponse(1, "provider", "model", llm.ChatResponse{}, errors.New("secret"))
	tr.Compaction(1, nil, nil, 0)
	tr.ToolCall(1, "id", "Read", "{}")
	tr.ToolResult(1, "id", "Read", "text", false, 4, false)
	tr.SummarizerRequest(SummarizerRequestEvent{})
	tr.SummarizerResponse(SummarizerResponseEvent{})
	tr.DispatchDone(DispatchDoneEvent{})
	tr.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("disabled trace created artifacts")
	}
	if From(context.Background()) != nil {
		t.Fatal("trace leaked into untraced context")
	}
}

func TestRejectUnsafeDirectory(t *testing.T) {
	for _, kind := range []string{"permissions", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := setup(t)
			if kind == "permissions" {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
			}
			if tr := Begin("child", "parent"); tr != nil {
				tr.Close()
				t.Fatal("accepted insecure trace directory")
			}
		})
	}
}

func TestCaptureFidelityAndCleanup(t *testing.T) {
	dir := setup(t)
	tr := Begin("child", "parent")
	if tr == nil {
		t.Fatal("not enabled")
	}
	defer tr.Close()
	ctx := WithTrace(context.Background(), tr)
	if From(ctx) != tr || From(context.Background()) != nil {
		t.Fatal("context isolation failed")
	}
	history := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "implement approved task"}, {Type: llm.BlockImage, ImageData: "PRIVATE_IMAGE", ImageURL: "https://secret", MediaType: "image/png"}, {Type: llm.BlockReasoning, ReasoningData: "PRIVATE_REASONING"}}}}
	temp := 0.25
	tr.ModelRequest(1, "probe", llm.ChatRequest{Model: "model", System: "system", Messages: history, MaxTokens: 321, Temperature: &temp, Tools: []llm.Tool{{Name: "Read", Description: "read evidence", Schema: json.RawMessage(`{"type":"object"}`)}}}, BudgetView{MessageTokens: 123})
	after := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "summary with evidence"}}}}
	tr.Compaction(2, history, after, 11)
	tr.SummarizerRequest(SummarizerRequestEvent{RequestID: "summary-1", Prompt: "exact summary prompt", Iteration: 2})
	tr.SummarizerResponse(SummarizerResponseEvent{RequestID: "summary-1", Output: "exact summary output", Iteration: 2})
	tr.ToolCall(2, "call1", "Read", `{"path":"evidence"}`)
	tr.ToolResult(2, "call1", "Read", "evidence [truncated]", false, 100, true)
	tr.ModelResponse(2, "probe", "model", llm.ChatResponse{StopReason: "end_turn", InputTokens: 42, Blocks: []llm.Block{{Type: llm.BlockText, Text: "response"}}}, errors.New("Authorization: Bearer TRANSPORT_SECRET"))
	tr.Close()
	tr.Close()
	tr.DispatchStart(DispatchStartEvent{}) // late events must be harmless
	if history[0].Blocks[1].ImageData != "PRIVATE_IMAGE" || history[0].Blocks[2].ReasoningData != "PRIVATE_REASONING" {
		t.Fatal("trace mutated provider input")
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory permissions: %v %v", info, err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("trace files: %v %v", files, err)
	}
	info, err = os.Stat(files[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file permissions: %v %v", info, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, bad := range []string{"PRIVATE_IMAGE", "PRIVATE_REASONING", "https://secret", "TRANSPORT_SECRET", "Authorization"} {
		if strings.Contains(text, bad) {
			t.Fatalf("unsafe payload captured: %s", bad)
		}
	}
	for _, want := range []string{"implement approved task", "summary with evidence", "exact summary prompt", "exact summary output", "evidence [truncated]", "end_turn", "response", "read evidence", "\"temperature\":0.25", "\"message_tokens\":123"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing evidence: %s", want)
		}
	}
	for i, line := range strings.Split(strings.TrimSpace(text), "\n") {
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Seq != i+1 || rec.Dispatch != "child" {
			t.Fatalf("bad correlation: %+v", rec)
		}
	}
}

func TestLiveArmFile(t *testing.T) {
	dir := setup(t)
	t.Setenv(EnableEnv, "")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "armed")
	if err := os.WriteFile(path, []byte("parent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if tr := Begin("wrong", "other"); tr != nil {
		tr.Close()
		t.Fatal("wrong parent captured")
	}
	tr := Begin("right", "parent")
	if tr == nil {
		t.Fatal("live arming failed")
	}
	tr.Close()
	if tr := Begin("another", "parent"); tr != nil {
		tr.Close()
		t.Fatal("live arm captured twice")
	}
}

func TestUnsafeArmFileIsIgnored(t *testing.T) {
	for _, kind := range []string{"permissions", "symlink", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			dir := setup(t)
			t.Setenv(EnableEnv, "")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "armed")
			if kind == "symlink" {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("parent"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			} else {
				body := "parent"
				if kind == "oversize" {
					body += strings.Repeat(" ", 257)
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "permissions" {
					if err := os.Chmod(path, 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tr := Begin("child", "parent"); tr != nil {
				tr.Close()
				t.Fatal("unsafe arming file accepted")
			}
		})
	}
}
