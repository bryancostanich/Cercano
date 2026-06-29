package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

// fakeCoproc returns a Services with a RunCoproc stub that records the last
// prompt and returns a fixed reply.
func fakeCoproc(t *testing.T, reply string) (capabilities.Services, *string) {
	t.Helper()
	var captured string
	svc := capabilities.Services{
		RunCoproc: func(_ context.Context, prompt, _ string) (string, error) {
			captured = prompt
			return reply, nil
		},
	}
	return svc, &captured
}

func callWith(t *testing.T, svc capabilities.Services, args any) *capabilities.Call {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return &capabilities.Call{Args: raw, WorkDir: "/proj", Svc: svc}
}

// ---- Summarize ----

func TestSummarize_Meta(t *testing.T) {
	c := Summarize()
	if c.Name() != "summarize" {
		t.Errorf("Name() = %q, want %q", c.Name(), "summarize")
	}
	if c.Tier() != capabilities.TierR {
		t.Errorf("Tier() = %q, want TierR", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if !c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("missing SurfaceMCP")
	}
	ca, ok := c.(capabilities.ContextAware)
	if !ok {
		t.Fatal("Summarize does not implement ContextAware")
	}
	if !ca.WantsProjectContext() {
		t.Error("WantsProjectContext() = false, want true")
	}
}

func TestSummarize_Execute_DefaultLength(t *testing.T) {
	svc, captured := fakeCoproc(t, "summary result")
	call := callWith(t, svc, map[string]string{"text": "hello world"})
	res, err := Summarize().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "Summarize the following text in one paragraph") {
		t.Errorf("prompt missing expected fragment; got: %q", *captured)
	}
	if !strings.Contains(*captured, "Text to summarize:") {
		t.Errorf("prompt missing 'Text to summarize:'; got: %q", *captured)
	}
	if !strings.Contains(*captured, "hello world") {
		t.Errorf("prompt missing content; got: %q", *captured)
	}
	if !strings.Contains(res.Text, "summary result") {
		t.Errorf("result text missing reply; got: %q", res.Text)
	}
}

func TestSummarize_Execute_Brief(t *testing.T) {
	svc, captured := fakeCoproc(t, "short")
	call := callWith(t, svc, map[string]string{"text": "some text", "max_length": "brief"})
	_, err := Summarize().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "1-2 sentences") {
		t.Errorf("brief prompt missing '1-2 sentences'; got: %q", *captured)
	}
}

func TestSummarize_Execute_Detailed(t *testing.T) {
	svc, captured := fakeCoproc(t, "long")
	call := callWith(t, svc, map[string]string{"text": "some text", "max_length": "detailed"})
	_, err := Summarize().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "multiple paragraphs covering all key points") {
		t.Errorf("detailed prompt missing expected fragment; got: %q", *captured)
	}
}

func TestSummarize_Execute_FilePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(f, []byte("file content here"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, captured := fakeCoproc(t, "done")
	call := callWith(t, svc, map[string]string{"file_path": f})
	_, err := Summarize().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "file content here") {
		t.Errorf("prompt missing file content; got: %q", *captured)
	}
}

func TestSummarize_Execute_NeitherErrors(t *testing.T) {
	svc, _ := fakeCoproc(t, "")
	call := callWith(t, svc, map[string]string{})
	_, err := Summarize().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error when neither text nor file_path provided")
	}
}

// ---- Extract ----

func TestExtract_Meta(t *testing.T) {
	c := Extract()
	if c.Name() != "extract" {
		t.Errorf("Name() = %q, want %q", c.Name(), "extract")
	}
	if c.Tier() != capabilities.TierR {
		t.Errorf("Tier() = %q, want TierR", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if !c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("missing SurfaceMCP")
	}
	ca, ok := c.(capabilities.ContextAware)
	if !ok {
		t.Fatal("Extract does not implement ContextAware")
	}
	if !ca.WantsProjectContext() {
		t.Error("WantsProjectContext() = false, want true")
	}
}

func TestExtract_Execute_Basic(t *testing.T) {
	svc, captured := fakeCoproc(t, "extracted")
	call := callWith(t, svc, map[string]string{"text": "source text", "query": "find dates"})
	res, err := Extract().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "find dates") {
		t.Errorf("prompt missing query; got: %q", *captured)
	}
	if !strings.Contains(*captured, "Extract the following") {
		t.Errorf("prompt missing 'Extract the following'; got: %q", *captured)
	}
	if !strings.Contains(*captured, "source text") {
		t.Errorf("prompt missing content; got: %q", *captured)
	}
	if !strings.Contains(res.Text, "extracted") {
		t.Errorf("result missing reply; got: %q", res.Text)
	}
}

func TestExtract_Execute_FilePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(f, []byte("file data"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, captured := fakeCoproc(t, "ok")
	call := callWith(t, svc, map[string]string{"file_path": f, "query": "something"})
	_, err := Extract().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "file data") {
		t.Errorf("prompt missing file content; got: %q", *captured)
	}
}

func TestExtract_Execute_MissingQueryErrors(t *testing.T) {
	svc, _ := fakeCoproc(t, "")
	call := callWith(t, svc, map[string]string{"text": "some text"})
	_, err := Extract().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error when query is missing")
	}
	if !strings.Contains(err.Error(), "query") {
		t.Errorf("error message should mention 'query'; got: %v", err)
	}
}

func TestExtract_Execute_NeitherErrors(t *testing.T) {
	svc, _ := fakeCoproc(t, "")
	call := callWith(t, svc, map[string]string{"query": "find something"})
	_, err := Extract().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error when neither text nor file_path provided")
	}
}

// ---- Classify ----

func TestClassify_Meta(t *testing.T) {
	c := Classify()
	if c.Name() != "classify" {
		t.Errorf("Name() = %q, want %q", c.Name(), "classify")
	}
	if c.Tier() != capabilities.TierR {
		t.Errorf("Tier() = %q, want TierR", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if !c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("missing SurfaceMCP")
	}
	ca, ok := c.(capabilities.ContextAware)
	if !ok {
		t.Fatal("Classify does not implement ContextAware")
	}
	if !ca.WantsProjectContext() {
		t.Error("WantsProjectContext() = false, want true")
	}
}

func TestClassify_Execute_NoCategories(t *testing.T) {
	svc, captured := fakeCoproc(t, "Category: X\nConfidence: high\nReasoning: because")
	call := callWith(t, svc, map[string]string{"text": "some code"})
	_, err := Classify().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "Determine the most appropriate category.") {
		t.Errorf("prompt missing default category instruction; got: %q", *captured)
	}
	if !strings.Contains(*captured, "some code") {
		t.Errorf("prompt missing content; got: %q", *captured)
	}
}

func TestClassify_Execute_WithCategories(t *testing.T) {
	svc, captured := fakeCoproc(t, "ok")
	call := callWith(t, svc, map[string]string{"text": "x", "categories": "bug,feature,docs"})
	_, err := Classify().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "Choose from these categories: bug,feature,docs") {
		t.Errorf("prompt missing category instruction; got: %q", *captured)
	}
}

func TestClassify_Execute_NeitherErrors(t *testing.T) {
	svc, _ := fakeCoproc(t, "")
	call := callWith(t, svc, map[string]string{})
	_, err := Classify().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error when neither text nor file_path provided")
	}
}

// ---- Explain ----

func TestExplain_Meta(t *testing.T) {
	c := Explain()
	if c.Name() != "explain" {
		t.Errorf("Name() = %q, want %q", c.Name(), "explain")
	}
	if c.Tier() != capabilities.TierR {
		t.Errorf("Tier() = %q, want TierR", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if !c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("missing SurfaceMCP")
	}
	ca, ok := c.(capabilities.ContextAware)
	if !ok {
		t.Fatal("Explain does not implement ContextAware")
	}
	if !ca.WantsProjectContext() {
		t.Error("WantsProjectContext() = false, want true")
	}
}

func TestExplain_Execute_Basic(t *testing.T) {
	svc, captured := fakeCoproc(t, "explanation")
	call := callWith(t, svc, map[string]string{"text": "func main() {}"})
	res, err := Explain().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*captured, "Explain the following code or text") {
		t.Errorf("prompt missing expected fragment; got: %q", *captured)
	}
	if !strings.Contains(*captured, "func main() {}") {
		t.Errorf("prompt missing content; got: %q", *captured)
	}
	if !strings.Contains(res.Text, "explanation") {
		t.Errorf("result missing reply; got: %q", res.Text)
	}
}

func TestExplain_Execute_NeitherErrors(t *testing.T) {
	svc, _ := fakeCoproc(t, "")
	call := callWith(t, svc, map[string]string{})
	_, err := Explain().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error when neither text nor file_path provided")
	}
}
