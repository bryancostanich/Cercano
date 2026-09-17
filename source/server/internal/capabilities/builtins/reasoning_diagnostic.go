package builtins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/reasoningexperiment"
)

type reasoningDiagnosticCap struct{}

func ReasoningDiagnostic() capabilities.Capability { return reasoningDiagnosticCap{} }
func (reasoningDiagnosticCap) Name() string        { return "reasoning_diagnostic" }
func (reasoningDiagnosticCap) Description() string {
	return "Run bounded paid drop/preserve reasoning-continuation trials against one existing chat-completions profile. Always confirms; advertised in development mode. Reads a local JSON experiment file with explicit model, token/request/time limits and deterministic recorded tool results. Never executes model-requested tools, retries, falls back, or returns raw prompts/reasoning. Returns structural hashes, finishes and cycle observations, not a correctness verdict. See docs/bugs/reasoning-continuation-diagnostic.md."
}
func (reasoningDiagnosticCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (reasoningDiagnosticCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (reasoningDiagnosticCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","properties":{"input_path":{"type":"string","description":"Path to the bounded experiment JSON, relative to the conversation working directory or absolute."}},"required":["input_path"],"additionalProperties":false}`)
}
func (reasoningDiagnosticCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a struct {
		InputPath string `json:"input_path"`
	}
	d := json.NewDecoder(bytes.NewReader(call.Args))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil || d.Decode(new(any)) != io.EOF || a.InputPath == "" {
		return nil, errors.New("input_path is required")
	}
	if call.Svc.ReasoningDiagnostic == nil {
		return nil, errors.New("reasoning diagnostic service unavailable")
	}
	path := a.InputPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(call.WorkDir, path)
	}
	f, err := openDiagnosticInput(path)
	if err != nil {
		return nil, errors.New("cannot open diagnostic input")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > reasoningexperiment.MaxInputBytes {
		return nil, errors.New("diagnostic input must be a regular file at most 8 MiB")
	}
	spec, err := reasoningexperiment.Decode(f)
	if err != nil {
		return nil, err
	}
	report, err := call.Svc.ReasoningDiagnostic(ctx, spec)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	return &capabilities.Result{Type: capabilities.ResultJSON, JSON: out}, nil
}
