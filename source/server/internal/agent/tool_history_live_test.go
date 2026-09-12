package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	oa "cercano/source/server/internal/llm/openai"
)

// This opt-in diagnostic uses only synthetic fixtures and an explicitly supplied
// endpoint. It deliberately bypasses dispatch routing to isolate history format.
func TestLiveToolHistoryComparison(t *testing.T) {
	endpoint := os.Getenv("CERCANO_TOOL_HISTORY_ENDPOINT")
	if endpoint == "" {
		t.Skip("set CERCANO_TOOL_HISTORY_ENDPOINT to opt into live diagnostic")
	}
	artifacts, err := os.MkdirTemp("", "cercano-tool-history-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("artifacts: %s", artifacts)
	for pair := 0; pair < 3; pair++ {
		fixture := t.TempDir()
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			t.Fatal(err)
		}
		second := hex.EncodeToString(nonce) + ".txt"
		if _, err := rand.Read(nonce); err != nil {
			t.Fatal(err)
		}
		code := hex.EncodeToString(nonce)
		for name, body := range map[string]string{"start.txt": "Read " + second + " to obtain the code.", second: code} {
			if err := os.WriteFile(filepath.Join(fixture, name), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, flat := range []bool{true, false} {
			t.Run(fmt.Sprintf("pair%d/flatten=%t", pair, flat), func(t *testing.T) {
				f, err := os.Create(filepath.Join(artifacts, fmt.Sprintf("pair%d-flat%t.jsonl", pair, flat)))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				trace := &historyLiveTrace{enc: json.NewEncoder(f)}
				read := &historyLiveRead{root: fixture, trace: trace}
				reg := agenttools.NewRegistry()
				reg.MustRegister(read)
				provider := &historyLiveProvider{Provider: oa.NewClient(oa.Config{BaseURL: endpoint, Backend: "llama_server", Model: os.Getenv("CERCANO_TOOL_HISTORY_MODEL")}), trace: trace}
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				temp := 0.0
				result, err := RunToolLoop(ctx, ToolLoopInput{Provider: provider, Registry: reg, PreauthorizedTools: []string{"Read"}, System: "You are a bounded Cercano sub-agent. Complete the requested task using the granted tools.", UserInput: "Read start.txt. Follow the filename inside it to read the second file. Return only the exact code contained in the second file.", WorkDir: fixture, ConversationID: fmt.Sprintf("history-probe-%d-%t", pair, flat), Temperature: &temp, FlattenToolResults: flat, MaxIterations: 5, MaxTokensPerTurn: 512})
				trace.log("result", map[string]any{"result": result, "error": fmt.Sprint(err)})
				t.Logf("flat=%t iterations=%d successful_reads=%v final=%q error=%v", flat, result.Iterations, read.reads, result.FinalText, err)
				if err != nil || strings.TrimSpace(result.FinalText) != code || len(read.reads) != 2 || read.reads[0] != "start.txt" || read.reads[1] != second {
					t.Errorf("expected two ordered reads and exact code")
				}
			})
		}
	}
}

type historyLiveTrace struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (l *historyLiveTrace) log(kind string, value any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(map[string]any{"kind": kind, "value": value})
}

type historyLiveProvider struct {
	inference.Provider
	trace *historyLiveTrace
}

func (p *historyLiveProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.trace.log("request", req)
	s, err := p.Provider.StreamChat(ctx, req)
	if err != nil {
		p.trace.log("provider_error", err.Error())
		return nil, err
	}
	return &historyLiveStream{StreamReader: s, trace: p.trace}, nil
}

type historyLiveStream struct {
	llm.StreamReader
	trace *historyLiveTrace
}

func (s *historyLiveStream) Next() (llm.StreamEvent, bool, error) {
	ev, ok, err := s.StreamReader.Next()
	s.trace.log("event", map[string]any{"event": ev, "ok": ok, "error": fmt.Sprint(err)})
	return ev, ok, err
}

type historyLiveRead struct {
	root  string
	trace *historyLiveTrace
	reads []string
}

func (*historyLiveRead) Name() string { return "Read" }
func (*historyLiveRead) Description() string {
	return "Read a UTF-8 fixture file by its relative filename."
}
func (*historyLiveRead) Permission() agenttools.Permission { return agenttools.PermR }
func (*historyLiveRead) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
}
func (r *historyLiveRead) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	r.trace.log("tool_call", json.RawMessage(args))
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Path == "" || filepath.Base(a.Path) != a.Path || a.Path == "." || a.Path == ".." {
		return nil, fmt.Errorf("only fixture filenames permitted")
	}
	b, err := os.ReadFile(filepath.Join(r.root, a.Path))
	if err != nil {
		return nil, err
	}
	r.reads = append(r.reads, a.Path)
	r.trace.log("tool_result", string(b))
	return &agenttools.Result{Type: agenttools.ResultText, Text: string(b)}, nil
}
