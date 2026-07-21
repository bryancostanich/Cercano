package trajectory

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
)

func TestExporterWritesBundleWithToolArtifactAndSubagent(t *testing.T) {
	ctx := context.Background()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if err := store.EnsureConversation(ctx, "mainconv", "/Users/example/project", "claude-test"); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, conversation.Turn{ID: "t1", ConversationID: "mainconv", Role: "user", Content: "please inspect", CreatedAt: base}); err != nil {
		t.Fatal(err)
	}
	assistantBlocks := `[{"type":"text","text":"I will delegate."},{"type":"tool_use","id":"call_dispatch","name":"dispatch","input":{"task":"inspect"}}]`
	if err := store.Append(ctx, conversation.Turn{ID: "t2", ConversationID: "mainconv", Role: "assistant", BlocksJSON: assistantBlocks, TokensIn: 10, TokensOut: 5, CreatedAt: base.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	resultBlocks := `[{"type":"tool_result","tool_use_id":"call_dispatch","content":"subagent found api_key=supersecret","is_error":false}]`
	if err := store.Append(ctx, conversation.Turn{ID: "t3", ConversationID: "mainconv", Role: "user", BlocksJSON: resultBlocks, CreatedAt: base.Add(2*time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSubagentConversation(ctx, "childconv", "mainconv", "/Users/example/project", "qwen", []string{"Read"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, conversation.Turn{ID: "c1", ConversationID: "childconv", Role: "user", Content: "inspect", CreatedAt: base.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, conversation.Turn{ID: "c2", ConversationID: "childconv", Role: "assistant", Content: "done", CreatedAt: base.Add(2*time.Second)}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "bundle")
	res, err := (Exporter{Store: store}).Export(ctx, Options{ConversationID: "mainconv", OutPath: out, Redaction: RedactDefault, Now: base, Version: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ArtifactCount != 1 {
		t.Fatalf("artifact count = %d, want 1", res.ArtifactCount)
	}
	if res.SubagentCount != 1 {
		t.Fatalf("subagent count = %d, want 1", res.SubagentCount)
	}
	if err := ValidateBundle(out); err != nil {
		t.Fatal(err)
	}
	var tr Trajectory
	mustReadJSON(t, filepath.Join(out, "trajectory.json"), &tr)
	if tr.SchemaVersion != ATIFVersion || len(tr.Steps) != 2 {
		t.Fatalf("trajectory schema/steps = %s/%d", tr.SchemaVersion, len(tr.Steps))
	}
	if len(tr.Steps[1].ToolCalls) != 1 || tr.Steps[1].ToolCalls[0].FunctionName != "dispatch" {
		t.Fatalf("missing dispatch tool call: %#v", tr.Steps[1].ToolCalls)
	}
	if tr.Steps[1].Observation == nil || len(tr.Steps[1].Observation.Results) < 2 {
		t.Fatalf("missing observations: %#v", tr.Steps[1].Observation)
	}
	artifact := filepath.Join(out, "artifacts/tool-results/step-0002-call-call_dispatch.txt")
	body, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "subagent found api_key=[REDACTED]" {
		t.Fatalf("artifact body = %q", string(body))
	}
	if _, err := os.Stat(filepath.Join(out, "subagents/dispatch-0001/trajectory.json")); err != nil {
		t.Fatal(err)
	}
}

func TestExporterZipContainsTopLevelBundle(t *testing.T) {
	ctx := context.Background()
	store, err := conversation.Open(":memory:")
	if err != nil { t.Fatal(err) }
	defer store.Close()
	if err := store.EnsureConversation(ctx, "conv", "/tmp/project", "model"); err != nil { t.Fatal(err) }
	if err := store.Append(ctx, conversation.Turn{ID:"t1", ConversationID:"conv", Role:"user", Content:"hello", CreatedAt:time.Now()}); err != nil { t.Fatal(err) }
	zipPath := filepath.Join(t.TempDir(), "my-export.zip")
	if _, err := (Exporter{Store:store}).Export(ctx, Options{ConversationID:"conv", OutPath:zipPath, Format:FormatZip, Redaction:RedactNone}, nil); err != nil { t.Fatal(err) }
	zr, err := zip.OpenReader(zipPath)
	if err != nil { t.Fatal(err) }
	defer zr.Close()
	want := "my-export/trajectory.json"
	for _, f := range zr.File { if f.Name == want { return } }
	t.Fatalf("zip missing %s", want)
}

func mustReadJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if err := json.Unmarshal(b, v); err != nil { t.Fatal(err) }
}
