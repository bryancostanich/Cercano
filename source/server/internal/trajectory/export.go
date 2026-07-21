package trajectory

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

const (
	BundleFormat  = "cercano-trajectory-bundle"
	BundleVersion = 1
	ATIFVersion   = "ATIF-v1.7"
)

type Format string

const (
	FormatInfer     Format = "infer"
	FormatDirectory Format = "directory"
	FormatZip       Format = "zip"
)

type RedactionMode string

const (
	RedactDefault RedactionMode = "default"
	RedactNone    RedactionMode = "none"
)

type Options struct {
	ConversationID string
	OutPath        string
	Format         Format
	Redaction      RedactionMode
	IncludeLogs    bool
	Overwrite      bool
	Version        string
	Now            time.Time
	PreviewBytes   int
}

type Result struct {
	OutputPath    string
	BundleDir     string
	ManifestPath  string
	ArtifactCount int
	SubagentCount int
	Warnings      []string
}

type Progress func(phase, message string)

type Exporter struct{ Store conversation.Store }

func (e Exporter) Export(ctx context.Context, opts Options, progress Progress) (Result, error) {
	if e.Store == nil {
		return Result{}, errors.New("trajectory export: nil conversation store")
	}
	opts = normalizeOptions(opts)
	if opts.ConversationID == "" {
		return Result{}, errors.New("--conversation is required")
	}
	if opts.OutPath == "" {
		return Result{}, errors.New("--out is required")
	}
	if opts.Redaction != RedactDefault && opts.Redaction != RedactNone {
		return Result{}, fmt.Errorf("invalid redaction mode %q", opts.Redaction)
	}
	if opts.Format != FormatInfer && opts.Format != FormatDirectory && opts.Format != FormatZip {
		return Result{}, fmt.Errorf("invalid export format %q", opts.Format)
	}
	if progress == nil {
		progress = func(_, _ string) {}
	}

	info, err := e.Store.Get(ctx, opts.ConversationID)
	if err != nil {
		return Result{}, fmt.Errorf("get conversation: %w", err)
	}
	format := opts.Format
	if format == FormatInfer {
		if strings.EqualFold(filepath.Ext(opts.OutPath), ".zip") {
			format = FormatZip
		} else {
			format = FormatDirectory
		}
	}
	outPath, err := filepath.Abs(opts.OutPath)
	if err != nil {
		return Result{}, err
	}
	bundleRoot := outPath
	finalZip := ""
	if format == FormatZip {
		finalZip = outPath
		bundleRoot = strings.TrimSuffix(outPath, filepath.Ext(outPath))
	}
	if err := prepareOutput(bundleRoot, finalZip, opts.Overwrite); err != nil {
		return Result{}, err
	}

	b := &builder{store: e.Store, opts: opts, progress: progress, root: bundleRoot, manifest: Manifest{Format: BundleFormat, FormatVersion: BundleVersion, CreatedAt: opts.Now.UTC().Format(time.RFC3339), RootTrajectory: "trajectory.json", SchemaVersion: ATIFVersion, ConversationID: opts.ConversationID, BundleName: filepath.Base(bundleRoot), Redaction: Redaction{Mode: string(opts.Redaction)}}}
	if opts.Redaction == RedactDefault {
		b.manifest.Redaction.Warning = "Pattern-based redaction was applied. Review before sharing."
	}
	progress("prepare", "creating bundle directories")
	for _, d := range []string{"artifacts/tool-results", "metadata"} {
		if err := os.MkdirAll(filepath.Join(bundleRoot, d), 0o755); err != nil {
			return Result{}, err
		}
	}

	progress("load", "loading conversation turns")
	traj, err := b.buildTrajectory(ctx, info, "main", ".")
	if err != nil {
		return Result{}, err
	}
	progress("write", "writing root trajectory.json")
	if err := writeJSON(filepath.Join(bundleRoot, "trajectory.json"), traj); err != nil {
		return Result{}, err
	}
	progress("metadata", "writing raw conversation metadata")
	if err := b.writeMetadata(ctx, info); err != nil {
		return Result{}, err
	}
	progress("manifest", "writing manifest.json")
	sort.Slice(b.manifest.Artifacts, func(i, j int) bool { return b.manifest.Artifacts[i].Path < b.manifest.Artifacts[j].Path })
	sort.Slice(b.manifest.Subagents, func(i, j int) bool { return b.manifest.Subagents[i].Path < b.manifest.Subagents[j].Path })
	if err := writeJSON(filepath.Join(bundleRoot, "manifest.json"), b.manifest); err != nil {
		return Result{}, err
	}
	if err := ValidateBundle(bundleRoot); err != nil {
		return Result{}, err
	}
	res := Result{OutputPath: bundleRoot, BundleDir: bundleRoot, ManifestPath: filepath.Join(bundleRoot, "manifest.json"), ArtifactCount: len(b.manifest.Artifacts), SubagentCount: len(b.manifest.Subagents), Warnings: b.warnings}
	if format == FormatZip {
		progress("zip", "creating zip archive")
		if err := zipDir(bundleRoot, finalZip); err != nil {
			return Result{}, err
		}
		_ = os.RemoveAll(bundleRoot)
		res.OutputPath = finalZip
		res.BundleDir = ""
		res.ManifestPath = filepath.Join(strings.TrimSuffix(filepath.Base(finalZip), filepath.Ext(finalZip)), "manifest.json")
	}
	progress("complete", "trajectory export complete")
	return res, nil
}

func normalizeOptions(o Options) Options {
	if o.Format == "" {
		o.Format = FormatInfer
	}
	if o.Redaction == "" {
		o.Redaction = RedactDefault
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.PreviewBytes <= 0 {
		o.PreviewBytes = 4096
	}
	if o.Version == "" {
		o.Version = "dev"
	}
	return o
}
func prepareOutput(bundleRoot, zipPath string, overwrite bool) error {
	for _, p := range []string{bundleRoot, zipPath} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			if !overwrite {
				return fmt.Errorf("output exists: %s (use --overwrite)", p)
			}
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
	}
	return os.MkdirAll(bundleRoot, 0o755)
}

type builder struct {
	store            conversation.Store
	opts             Options
	progress         Progress
	root             string
	prefix           string
	manifest         Manifest
	warnings         []string
	dispatchChildIdx int
}

func (b *builder) buildTrajectory(ctx context.Context, info conversation.Info, trajectoryID, relRoot string) (Trajectory, error) {
	turns, err := b.store.GetTurns(ctx, info.ID)
	if err != nil {
		return Trajectory{}, fmt.Errorf("get turns %s: %w", info.ID, err)
	}
	children, _ := b.store.ListChildren(ctx, info.ID)
	childQueue := append([]conversation.Info(nil), children...)
	steps := make([]Step, 0, len(turns))
	var lastAgent *Step
	for _, t := range turns {
		blocks := parseBlocks(t.BlocksJSON)
		if len(blocks) > 0 && onlyToolResults(blocks) && lastAgent != nil {
			b.attachToolResults(lastAgent, t, blocks, relRoot)
			continue
		}
		step := Step{StepID: len(steps) + 1, Timestamp: formatTime(t.CreatedAt), Source: atifSource(t.Role), Message: redact(b.opts.Redaction, t.Content), Extra: map[string]any{"cercano": map[string]any{"turn_id": t.ID, "role": t.Role}}}
		if t.TokensIn > 0 || t.TokensOut > 0 || t.LatencyMs > 0 {
			step.Metrics = &Metrics{PromptTokens: omitZero(t.TokensIn), CompletionTokens: omitZero(t.TokensOut), Extra: map[string]any{"latency_ms": t.LatencyMs}}
		}
		if len(blocks) > 0 {
			b.applyBlocks(&step, t, blocks, relRoot, &childQueue)
		}
		steps = append(steps, step)
		if step.Source == "agent" {
			lastAgent = &steps[len(steps)-1]
		}
	}
	tr := Trajectory{SchemaVersion: ATIFVersion, SessionID: info.ID, TrajectoryID: trajectoryID, Agent: Agent{Name: "cercano", Version: b.opts.Version, ModelName: info.Model}, Steps: steps, Extra: map[string]any{"cercano": map[string]any{"bundle_format": BundleFormat, "bundle_format_version": BundleVersion, "manifest_path": "manifest.json", "conversation_id": info.ID, "work_dir": redact(b.opts.Redaction, info.ProjectDir), "kind": info.Kind, "parent_id": info.ParentID}}}
	if len(steps) > 0 {
		tr.FinalMetrics = &FinalMetrics{TotalSteps: len(steps)}
	}
	// Export child subagents after parent steps so refs can point at files.
	if relRoot == "." {
		for i, child := range children {
			id := fmt.Sprintf("dispatch-%04d", i+1)
			dirRel := filepath.ToSlash(filepath.Join("subagents", id))
			dirAbs := filepath.Join(b.root, dirRel)
			for _, d := range []string{"artifacts/tool-results", "metadata"} {
				_ = os.MkdirAll(filepath.Join(dirAbs, d), 0o755)
			}
			subBuilder := &builder{store: b.store, opts: b.opts, progress: b.progress, root: dirAbs, manifest: Manifest{Format: BundleFormat, FormatVersion: BundleVersion, CreatedAt: b.opts.Now.UTC().Format(time.RFC3339), RootTrajectory: "trajectory.json", SchemaVersion: ATIFVersion, ConversationID: child.ID, BundleName: id, Redaction: b.manifest.Redaction}}
			sub, err := subBuilder.buildTrajectory(ctx, child, id, dirRel)
			if err != nil {
				b.warnings = append(b.warnings, err.Error())
				continue
			}
			_ = writeJSON(filepath.Join(dirAbs, "trajectory.json"), sub)
			_ = subBuilder.writeMetadata(ctx, child)
			sort.Slice(subBuilder.manifest.Artifacts, func(i, j int) bool {
				return subBuilder.manifest.Artifacts[i].Path < subBuilder.manifest.Artifacts[j].Path
			})
			_ = writeJSON(filepath.Join(dirAbs, "manifest.json"), subBuilder.manifest)
			b.manifest.Subagents = append(b.manifest.Subagents, SubagentEntry{TrajectoryID: id, Path: filepath.ToSlash(filepath.Join(dirRel, "trajectory.json"))})
		}
	}
	return tr, nil
}

func (b *builder) applyBlocks(step *Step, t conversation.Turn, blocks []llm.Block, relRoot string, childQueue *[]conversation.Info) {
	texts := []string{}
	for _, bl := range blocks {
		switch bl.Type {
		case llm.BlockText:
			if bl.Text != "" {
				texts = append(texts, redact(b.opts.Redaction, bl.Text))
			}
		case llm.BlockReasoning:
			step.ReasoningContent = redact(b.opts.Redaction, bl.Text)
		case llm.BlockToolUse:
			tc := ToolCall{ToolCallID: bl.ToolUseID, FunctionName: bl.ToolName, Arguments: rawObject(bl.ToolInput)}
			step.ToolCalls = append(step.ToolCalls, tc)
			if isDispatch(bl.ToolName) && len(*childQueue) > 0 {
				child := (*childQueue)[0]
				*childQueue = (*childQueue)[1:]
				id := fmt.Sprintf("dispatch-%04d", b.dispatchChildIdx+1)
				b.dispatchChildIdx++
				ref := SubagentTrajectoryRef{TrajectoryID: id, TrajectoryPath: filepath.ToSlash(filepath.Join("subagents", id, "trajectory.json"))}
				if step.Observation == nil {
					step.Observation = &Observation{}
				}
				step.Observation.Results = append(step.Observation.Results, ObservationResult{SourceCallID: bl.ToolUseID, Content: "Delegated work to subagent " + id + ".", SubagentTrajectoryRef: []SubagentTrajectoryRef{ref}, Extra: map[string]any{"cercano": map[string]any{"subagent_conversation_id": child.ID}}})
			}
		case llm.BlockToolResult:
			b.attachOneResult(step, t, bl, relRoot)
		}
	}
	if strings.TrimSpace(step.Message) == "" && len(texts) > 0 {
		step.Message = strings.Join(texts, "\n")
	}
}
func (b *builder) attachToolResults(step *Step, t conversation.Turn, blocks []llm.Block, relRoot string) {
	for _, bl := range blocks {
		if bl.Type == llm.BlockToolResult {
			b.attachOneResult(step, t, bl, relRoot)
		}
	}
}
func (b *builder) attachOneResult(step *Step, t conversation.Turn, bl llm.Block, relRoot string) {
	if step.Observation == nil {
		step.Observation = &Observation{}
	}
	content := redact(b.opts.Redaction, bl.Content)
	rel := filepath.ToSlash(filepath.Join("artifacts/tool-results", fmt.Sprintf("step-%04d-call-%s.txt", step.StepID, safeName(bl.ToolUseRef))))
	abs := filepath.Join(b.root, rel)
	_ = os.MkdirAll(filepath.Dir(abs), 0o755)
	_ = os.WriteFile(abs, []byte(content), 0o644)
	sum, size := fileHash(abs)
	b.manifest.Artifacts = append(b.manifest.Artifacts, Artifact{Path: rel, Kind: "tool_result", StepID: step.StepID, ToolCallID: bl.ToolUseRef, Bytes: size, SHA256: sum})
	preview := content
	if len(preview) > b.opts.PreviewBytes {
		preview = preview[:b.opts.PreviewBytes] + "\n[truncated in trajectory preview; see artifact]"
	}
	step.Observation.Results = append(step.Observation.Results, ObservationResult{SourceCallID: bl.ToolUseRef, Content: preview, Extra: map[string]any{"artifact_path": rel, "is_error": bl.IsError, "start_line": bl.StartLine}})
}

func (b *builder) writeMetadata(ctx context.Context, info conversation.Info) error {
	turns, err := b.store.GetTurns(ctx, info.ID)
	if err != nil {
		return err
	}
	meta := map[string]any{"id": info.ID, "title": info.Title, "project_dir": redact(b.opts.Redaction, info.ProjectDir), "model": info.Model, "started_at": formatTime(info.StartedAt), "last_turn_at": formatTime(info.LastTurnAt), "turn_count": info.TurnCount, "recap": redact(b.opts.Redaction, info.Recap), "kind": info.Kind, "parent_id": info.ParentID}
	if err := writeJSON(filepath.Join(b.root, "metadata/conversation.json"), meta); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(b.root, "metadata/conversation-turns.raw.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, t := range turns {
		row := map[string]any{"id": t.ID, "conversation_id": t.ConversationID, "role": t.Role, "content": redact(b.opts.Redaction, t.Content), "content_json": redact(b.opts.Redaction, t.BlocksJSON), "tokens_in": t.TokensIn, "tokens_out": t.TokensOut, "latency_ms": t.LatencyMs, "created_at": formatTime(t.CreatedAt)}
		_ = enc.Encode(row)
	}
	env := map[string]any{"exported_at": b.opts.Now.UTC().Format(time.RFC3339), "cercano_version": b.opts.Version}
	return writeJSON(filepath.Join(b.root, "metadata/environment.json"), env)
}

func parseBlocks(s string) []llm.Block {
	if s == "" {
		return nil
	}
	var b []llm.Block
	if json.Unmarshal([]byte(s), &b) != nil {
		return nil
	}
	return b
}
func onlyToolResults(blocks []llm.Block) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type != llm.BlockToolResult {
			return false
		}
	}
	return true
}
func atifSource(role string) string {
	switch role {
	case "assistant":
		return "agent"
	case "system":
		return "system"
	default:
		return "user"
	}
}
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
func omitZero(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
func rawObject(r json.RawMessage) map[string]any {
	var m map[string]any
	if len(r) > 0 && json.Unmarshal(r, &m) == nil && m != nil {
		return m
	}
	return map[string]any{}
}
func isDispatch(name string) bool {
	n := strings.ToLower(name)
	return n == "dispatch" || n == "workflow"
}
func safeName(s string) string {
	if s == "" {
		return "unknown"
	}
	re := regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	out := re.ReplaceAllString(s, "-")
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
func fileHash(path string) (string, int64) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()
	h := sha256.New()
	n, _ := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n
}

var secretPatterns = []*regexp.Regexp{regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|bearer)\s*[:=]\s*['"]?[^\s'",}]+`), regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`), regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)}

func redact(mode RedactionMode, s string) string {
	if mode == RedactNone || s == "" {
		return s
	}
	out := s
	for _, re := range secretPatterns {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			parts := regexp.MustCompile(`[:=]`).Split(m, 2)
			if len(parts) == 2 {
				return parts[0] + "=[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = strings.ReplaceAll(out, home, "~")
	}
	return out
}

func zipDir(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	base := filepath.Base(src)
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.Dir(src), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(base, strings.TrimPrefix(rel, base)))
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
}
