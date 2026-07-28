package worker

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/pkg/config"
)

// ─── LLMMessage round-trips ──────────────────────────────────────────────────

// TestMarshalMessageRoundTrip verifies that MarshalMessage→UnmarshalMessage is
// lossless for every block kind used by the tool loop and persistence layer.
// Fidelity is proven by comparing json.Marshal(original.Blocks) to
// json.Marshal(roundtripped.Blocks) — the same encoding the DB uses.
func TestMarshalMessageRoundTrip(t *testing.T) {
	toolInput := json.RawMessage(`{"path":"/tmp/foo","content":"hello world"}`)

	msg := llm.Message{
		Role: llm.RoleAssistant,
		Blocks: []llm.Block{
			// text block
			{Type: llm.BlockText, Text: "Here is the result"},
			// tool_use block
			{
				Type:      llm.BlockToolUse,
				ToolUseID: "toolu_abc123",
				ToolName:  "write_file",
				ToolInput: toolInput,
			},
			// tool_result block
			{
				Type:       llm.BlockToolResult,
				ToolUseRef: "toolu_abc123",
				Content:    "wrote 11 bytes",
				IsError:    false,
				StartLine:  42,
			},
			// tool_result error block
			{
				Type:       llm.BlockToolResult,
				ToolUseRef: "toolu_xyz",
				Content:    "permission denied",
				IsError:    true,
			},
			// image block (base64)
			{
				Type:      llm.BlockImage,
				MediaType: "image/png",
				ImageData: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwADhQGAWjR9awAAAABJRU5ErkJggg==",
			},
			// image block (URL)
			{
				Type:     llm.BlockImage,
				ImageURL: "https://example.com/pic.png",
			},
			// reasoning block
			{
				Type:          llm.BlockReasoning,
				ReasoningID:   "r_001",
				ReasoningData: "opaque-encrypted-bytes",
			},
		},
	}

	p, err := MarshalMessage(msg)
	if err != nil {
		t.Fatalf("MarshalMessage: %v", err)
	}

	got, err := UnmarshalMessage(p)
	if err != nil {
		t.Fatalf("UnmarshalMessage: %v", err)
	}

	if got.Role != msg.Role {
		t.Errorf("Role: got %q want %q", got.Role, msg.Role)
	}

	// Compare via JSON marshaling of Blocks — same encoding the DB uses.
	origJSON, err := json.Marshal(msg.Blocks)
	if err != nil {
		t.Fatalf("marshal original blocks: %v", err)
	}
	gotJSON, err := json.Marshal(got.Blocks)
	if err != nil {
		t.Fatalf("marshal roundtripped blocks: %v", err)
	}
	if !bytes.Equal(origJSON, gotJSON) {
		t.Errorf("blocks_json mismatch:\n  orig: %s\n   got: %s", origJSON, gotJSON)
	}

	// Also verify the proto's blocks_json field matches what we just computed.
	if !bytes.Equal(p.BlocksJson, origJSON) {
		t.Errorf("proto BlocksJson does not match json.Marshal(original.Blocks)")
	}
}

// TestMarshalMessageEmpty verifies empty/nil blocks round-trip cleanly.
func TestMarshalMessageEmpty(t *testing.T) {
	msg := llm.Message{Role: llm.RoleUser, Blocks: nil}
	p, err := MarshalMessage(msg)
	if err != nil {
		t.Fatalf("MarshalMessage: %v", err)
	}
	got, err := UnmarshalMessage(p)
	if err != nil {
		t.Fatalf("UnmarshalMessage: %v", err)
	}
	if got.Role != llm.RoleUser {
		t.Errorf("Role: got %q want %q", got.Role, llm.RoleUser)
	}
	if len(got.Blocks) != 0 {
		t.Errorf("Blocks: got %d blocks want 0", len(got.Blocks))
	}
}

// TestMarshalMessageUserText verifies a simple user text message.
func TestMarshalMessageUserText(t *testing.T) {
	msg := llm.Message{
		Role:   llm.RoleUser,
		Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}},
	}
	p, err := MarshalMessage(msg)
	if err != nil {
		t.Fatalf("MarshalMessage: %v", err)
	}
	// content should be the concatenated text
	if p.Content != "hello" {
		t.Errorf("Content: got %q want %q", p.Content, "hello")
	}
	got, err := UnmarshalMessage(p)
	if err != nil {
		t.Fatalf("UnmarshalMessage: %v", err)
	}
	if len(got.Blocks) != 1 || got.Blocks[0].Text != "hello" {
		t.Errorf("Blocks: unexpected %+v", got.Blocks)
	}
}

// ─── WorkerEvent round-trips ─────────────────────────────────────────────────

// TestMarshalEventRoundTrip verifies that MarshalEvent→UnmarshalEvent is
// lossless for every EventKind. This locks the wire contract so future code
// changes can't silently drop fields.
func TestMarshalEventRoundTrip(t *testing.T) {
	cases := []runner.Event{
		{
			Kind:    runner.EventRouteSelected,
			Model:   "claude-opus-4-5",
			IsCloud: true,
			Notice:  "fell back from open",
		},
		{
			Kind: runner.EventToken,
			Text: "hello world",
		},
		{
			Kind: runner.EventProgress,
			Text: "running tool...",
		},
		{
			Kind:      runner.EventToolUseStart,
			ToolUseID: "toolu_001",
			ToolName:  "bash",
		},
		{
			Kind:        runner.EventToolUseStop,
			ToolUseID:   "toolu_001",
			ArgsSummary: "echo hello",
		},
		{
			Kind:      runner.EventToolExecStart,
			ToolUseID: "toolu_001",
		},
		{
			Kind:      runner.EventToolExecComplete,
			ToolUseID: "toolu_001",
			Summary:   "wrote file",
			Detail:    "wrote 42 bytes to /tmp/x",
			StartLine: 7,
			IsError:   false,
		},
		{
			Kind:      runner.EventToolExecComplete,
			ToolUseID: "toolu_002",
			IsError:   true,
			Detail:    "permission denied",
		},
		{
			Kind:         runner.EventWatchdog,
			WatchdogKind: "challenge",
			Detail:       "commit-checkpoint",
			Summary:      "Are you sure?",
		},
		{
			Kind:         runner.EventWatchdog,
			WatchdogKind: "echo",
			Summary:      "echoed line",
			Thread:       "watchdog",
		},
		{
			Kind:   runner.EventDone,
			Notice: "cross-tier fallback",
			Result: runner.Result{
				FinalText:    "done text",
				Model:        "claude-opus-4-5",
				IsCloud:      true,
				InputTokens:  1234,
				OutputTokens: 567,
				Notice:       "cross-tier fallback",
			},
		},
		{
			Kind:             runner.EventSubAgent,
			SubAgentID:       "a",
			SubAgentParentID: "p",
			SubAgentTitle:    "t",
			SubAgentKind:     "explore",
			GrantedTools:     []string{"Read"},
			IgnoredTools:     []string{"Bash"},
		},
	}

	for _, orig := range cases {
		p := MarshalEvent(orig)
		got := UnmarshalEvent(p)

		if got.Kind != orig.Kind {
			t.Errorf("kind %v: Kind mismatch: got %v", orig.Kind, got.Kind)
		}
		if got.Text != orig.Text {
			t.Errorf("kind %v: Text: got %q want %q", orig.Kind, got.Text, orig.Text)
		}
		// For EventDone, Model/IsCloud are promoted from Result; skip the
		// generic flat-field check and verify them via Result below.
		if orig.Kind != runner.EventDone {
			if got.Model != orig.Model {
				t.Errorf("kind %v: Model: got %q want %q", orig.Kind, got.Model, orig.Model)
			}
			if got.IsCloud != orig.IsCloud {
				t.Errorf("kind %v: IsCloud: got %v want %v", orig.Kind, got.IsCloud, orig.IsCloud)
			}
		}
		if got.ToolUseID != orig.ToolUseID {
			t.Errorf("kind %v: ToolUseID: got %q want %q", orig.Kind, got.ToolUseID, orig.ToolUseID)
		}
		if got.ToolName != orig.ToolName {
			t.Errorf("kind %v: ToolName: got %q want %q", orig.Kind, got.ToolName, orig.ToolName)
		}
		if got.ArgsSummary != orig.ArgsSummary {
			t.Errorf("kind %v: ArgsSummary: got %q want %q", orig.Kind, got.ArgsSummary, orig.ArgsSummary)
		}
		if got.Detail != orig.Detail {
			t.Errorf("kind %v: Detail: got %q want %q", orig.Kind, got.Detail, orig.Detail)
		}
		if got.Summary != orig.Summary {
			t.Errorf("kind %v: Summary: got %q want %q", orig.Kind, got.Summary, orig.Summary)
		}
		if got.StartLine != orig.StartLine {
			t.Errorf("kind %v: StartLine: got %d want %d", orig.Kind, got.StartLine, orig.StartLine)
		}
		if got.IsError != orig.IsError {
			t.Errorf("kind %v: IsError: got %v want %v", orig.Kind, got.IsError, orig.IsError)
		}
		if got.WatchdogKind != orig.WatchdogKind {
			t.Errorf("kind %v: WatchdogKind: got %q want %q", orig.Kind, got.WatchdogKind, orig.WatchdogKind)
		}
		if got.Thread != orig.Thread {
			t.Errorf("kind %v: Thread: got %q want %q", orig.Kind, got.Thread, orig.Thread)
		}
		if got.Notice != orig.Notice {
			t.Errorf("kind %v: Notice: got %q want %q", orig.Kind, got.Notice, orig.Notice)
		}
		if got.SubAgentID != orig.SubAgentID {
			t.Errorf("kind %v: SubAgentID: got %q want %q", orig.Kind, got.SubAgentID, orig.SubAgentID)
		}
		if got.SubAgentParentID != orig.SubAgentParentID {
			t.Errorf("kind %v: SubAgentParentID: got %q want %q", orig.Kind, got.SubAgentParentID, orig.SubAgentParentID)
		}
		if got.SubAgentTitle != orig.SubAgentTitle {
			t.Errorf("kind %v: SubAgentTitle: got %q want %q", orig.Kind, got.SubAgentTitle, orig.SubAgentTitle)
		}
		if got.SubAgentKind != orig.SubAgentKind {
			t.Errorf("kind %v: SubAgentKind: got %q want %q", orig.Kind, got.SubAgentKind, orig.SubAgentKind)
		}
		if !slices.Equal(got.GrantedTools, orig.GrantedTools) {
			t.Errorf("kind %v: GrantedTools: got %v want %v", orig.Kind, got.GrantedTools, orig.GrantedTools)
		}
		if !slices.Equal(got.IgnoredTools, orig.IgnoredTools) {
			t.Errorf("kind %v: IgnoredTools: got %v want %v", orig.Kind, got.IgnoredTools, orig.IgnoredTools)
		}
		if orig.Kind == runner.EventDone {
			if got.Result.FinalText != orig.Result.FinalText {
				t.Errorf("kind Done: Result.FinalText: got %q want %q", got.Result.FinalText, orig.Result.FinalText)
			}
			if got.Result.Model != orig.Result.Model {
				t.Errorf("kind Done: Result.Model: got %q want %q", got.Result.Model, orig.Result.Model)
			}
			if got.Result.IsCloud != orig.Result.IsCloud {
				t.Errorf("kind Done: Result.IsCloud: got %v want %v", got.Result.IsCloud, orig.Result.IsCloud)
			}
			if got.Result.InputTokens != orig.Result.InputTokens {
				t.Errorf("kind Done: Result.InputTokens: got %d want %d", got.Result.InputTokens, orig.Result.InputTokens)
			}
			if got.Result.OutputTokens != orig.Result.OutputTokens {
				t.Errorf("kind Done: Result.OutputTokens: got %d want %d", got.Result.OutputTokens, orig.Result.OutputTokens)
			}
			if got.Result.Notice != orig.Result.Notice {
				t.Errorf("kind Done: Result.Notice: got %q want %q", got.Result.Notice, orig.Result.Notice)
			}
		}
	}
}

// ─── ConfigSnapshot round-trips ───────────────────────────────────────────────

// TestSnapshotConfigRoundTrip verifies that SnapshotConfig→ConfigFromSnapshot
// preserves all the fields the worker needs at turn time.
func TestSnapshotConfigRoundTrip(t *testing.T) {
	orig := config.Config{
		LocusMode:          "cloud_primary",
		ActiveCloudProfile: "myprofile",
		BackupCloudProfile: "backup",
		OllamaURL:          "http://localhost:11434",
		OpenRuntime:        "ollama",
		CloudProfiles: []config.CloudProfile{
			{
				Name:       "myprofile",
				Flavor:     "messages",
				Backend:    "",
				Route:      "meridian",
				BaseURL:    "https://api.anthropic.com",
				Model:      "claude-opus-4-5",
				Region:     "",
				AWSProfile: "",
			},
			{
				Name:       "backup",
				Flavor:     "chat_completions",
				Backend:    "openai",
				Route:      "direct",
				BaseURL:    "https://api.openai.com",
				Model:      "gpt-5.5",
				Region:     "us-east-1",
				AWSProfile: "prod",
			},
		},
		Models: config.ModelsConfig{
			Tiers: config.ModelTiers{
				MostCapable:   config.ModelTier{Open: "qwen3-72b"},
				Everyday:      config.ModelTier{Open: "qwen3-coder"},
				FastLight:     config.ModelTier{Open: "qwen3-1.7b"},
				FastLightText: config.ModelTier{Open: "phi4-mini"},
				Embedding:     config.ModelTier{Open: "nomic-embed-text"},
			},
		},
		Compaction: config.CompactionConfig{
			Enabled:               true,
			ActivationFloorTokens: 8000,
			SegmentTokens:         4000,
			VerbatimRecent:        10,
			HardOverridePct:       0.85,
			ElideToolResults:      true,
			LossyToolElision:      false,
		},
		Watchdog: config.WatchdogConfig{
			Enabled:       true,
			Mode:          "challenge-and-justify",
			Checks:        []string{"debug-loop", "commit-checkpoint"},
			Model:         "phi4-mini",
			EscalateAfter: 2,
			Echo:          true,
		},
		ToolLoop: config.ToolLoopConfig{MaxIterations: 37},
		ModelProfiles: config.ModelProfiles{
			Cloud: config.CloudCostProfiles{
				Providers: map[string]config.VendorCostTiers{
					"anthropic": {
						Economy:  config.CostTierModel{Model: "claude-haiku-3-5"},
						Standard: config.CostTierModel{Model: "claude-sonnet-4-5"},
						Premium:  config.CostTierModel{Model: "claude-opus-4-5"},
					},
				},
			},
		},
	}

	cred := "sk-ant-test-credential"
	p := SnapshotConfig(orig, cred)
	got := ConfigFromSnapshot(p)

	// Check locus + profile identity.
	if got.LocusMode != orig.LocusMode {
		t.Errorf("LocusMode: got %q want %q", got.LocusMode, orig.LocusMode)
	}
	if got.ActiveCloudProfile != orig.ActiveCloudProfile {
		t.Errorf("ActiveCloudProfile: got %q want %q", got.ActiveCloudProfile, orig.ActiveCloudProfile)
	}
	if got.BackupCloudProfile != orig.BackupCloudProfile {
		t.Errorf("BackupCloudProfile: got %q want %q", got.BackupCloudProfile, orig.BackupCloudProfile)
	}

	// Credential must survive.
	if p.ResolvedCredential != cred {
		t.Errorf("ResolvedCredential: got %q want %q", p.ResolvedCredential, cred)
	}

	// Active cloud profile fields.
	if len(got.CloudProfiles) == 0 {
		t.Fatal("CloudProfiles: empty after round-trip")
	}
	gp := got.CloudProfiles[0]
	op := orig.CloudProfiles[0]
	if gp.Flavor != op.Flavor {
		t.Errorf("CloudProfile.Flavor: got %q want %q", gp.Flavor, op.Flavor)
	}
	if gp.Route != op.Route {
		t.Errorf("CloudProfile.Route: got %q want %q", gp.Route, op.Route)
	}
	if gp.BaseURL != op.BaseURL {
		t.Errorf("CloudProfile.BaseURL: got %q want %q", gp.BaseURL, op.BaseURL)
	}
	if gp.Model != op.Model {
		t.Errorf("CloudProfile.Model: got %q want %q", gp.Model, op.Model)
	}

	// Backup cloud profile must survive as a second entry (active first, backup
	// second) so buildWorkerProviders can wrap active+backup in a fallback.
	if len(got.CloudProfiles) != 2 {
		t.Fatalf("CloudProfiles: got %d profiles, want 2 (active+backup)", len(got.CloudProfiles))
	}
	gbp := got.CloudProfiles[1]
	obp := orig.CloudProfiles[1]
	if gbp.Name != obp.Name {
		t.Errorf("backup CloudProfile.Name: got %q want %q", gbp.Name, obp.Name)
	}
	if gbp.Flavor != obp.Flavor {
		t.Errorf("backup CloudProfile.Flavor: got %q want %q", gbp.Flavor, obp.Flavor)
	}
	if gbp.Backend != obp.Backend {
		t.Errorf("backup CloudProfile.Backend: got %q want %q", gbp.Backend, obp.Backend)
	}
	if gbp.Route != obp.Route {
		t.Errorf("backup CloudProfile.Route: got %q want %q", gbp.Route, obp.Route)
	}
	if gbp.BaseURL != obp.BaseURL {
		t.Errorf("backup CloudProfile.BaseURL: got %q want %q", gbp.BaseURL, obp.BaseURL)
	}
	if gbp.Model != obp.Model {
		t.Errorf("backup CloudProfile.Model: got %q want %q", gbp.Model, obp.Model)
	}
	if gbp.Region != obp.Region {
		t.Errorf("backup CloudProfile.Region: got %q want %q", gbp.Region, obp.Region)
	}
	if gbp.AWSProfile != obp.AWSProfile {
		t.Errorf("backup CloudProfile.AWSProfile: got %q want %q", gbp.AWSProfile, obp.AWSProfile)
	}

	// Open runtime.
	if got.OllamaURL != orig.OllamaURL {
		t.Errorf("OllamaURL: got %q want %q", got.OllamaURL, orig.OllamaURL)
	}
	if got.OpenRuntime != orig.OpenRuntime {
		t.Errorf("OpenRuntime: got %q want %q", got.OpenRuntime, orig.OpenRuntime)
	}

	// Model tiers.
	gt, ot := got.Models.Tiers, orig.Models.Tiers
	checkTier := func(name, gotOpen, wantOpen string) {
		t.Helper()
		if gotOpen != wantOpen {
			t.Errorf("tier %s Open: got %q want %q", name, gotOpen, wantOpen)
		}
	}
	checkTier("MostCapable", gt.MostCapable.Open, ot.MostCapable.Open)
	checkTier("Everyday", gt.Everyday.Open, ot.Everyday.Open)
	checkTier("FastLight", gt.FastLight.Open, ot.FastLight.Open)
	checkTier("FastLightText", gt.FastLightText.Open, ot.FastLightText.Open)
	checkTier("Embedding", gt.Embedding.Open, ot.Embedding.Open)

	// Compaction.
	gc, oc := got.Compaction, orig.Compaction
	if gc.Enabled != oc.Enabled {
		t.Errorf("Compaction.Enabled: got %v want %v", gc.Enabled, oc.Enabled)
	}
	if gc.ActivationFloorTokens != oc.ActivationFloorTokens {
		t.Errorf("Compaction.ActivationFloorTokens: got %d want %d", gc.ActivationFloorTokens, oc.ActivationFloorTokens)
	}
	if gc.SegmentTokens != oc.SegmentTokens {
		t.Errorf("Compaction.SegmentTokens: got %d want %d", gc.SegmentTokens, oc.SegmentTokens)
	}
	if gc.VerbatimRecent != oc.VerbatimRecent {
		t.Errorf("Compaction.VerbatimRecent: got %d want %d", gc.VerbatimRecent, oc.VerbatimRecent)
	}
	if gc.HardOverridePct != oc.HardOverridePct {
		t.Errorf("Compaction.HardOverridePct: got %v want %v", gc.HardOverridePct, oc.HardOverridePct)
	}
	if gc.ElideToolResults != oc.ElideToolResults {
		t.Errorf("Compaction.ElideToolResults: got %v want %v", gc.ElideToolResults, oc.ElideToolResults)
	}
	if gc.LossyToolElision != oc.LossyToolElision {
		t.Errorf("Compaction.LossyToolElision: got %v want %v", gc.LossyToolElision, oc.LossyToolElision)
	}

	// Watchdog.
	gw, ow := got.Watchdog, orig.Watchdog
	if gw.Enabled != ow.Enabled {
		t.Errorf("Watchdog.Enabled: got %v want %v", gw.Enabled, ow.Enabled)
	}
	if gw.Mode != ow.Mode {
		t.Errorf("Watchdog.Mode: got %q want %q", gw.Mode, ow.Mode)
	}
	if len(gw.Checks) != len(ow.Checks) {
		t.Errorf("Watchdog.Checks: got %v want %v", gw.Checks, ow.Checks)
	} else {
		for i, c := range ow.Checks {
			if gw.Checks[i] != c {
				t.Errorf("Watchdog.Checks[%d]: got %q want %q", i, gw.Checks[i], c)
			}
		}
	}
	if gw.Model != ow.Model {
		t.Errorf("Watchdog.Model: got %q want %q", gw.Model, ow.Model)
	}
	if gw.EscalateAfter != ow.EscalateAfter {
		t.Errorf("Watchdog.EscalateAfter: got %d want %d", gw.EscalateAfter, ow.EscalateAfter)
	}
	if gw.Echo != ow.Echo {
		t.Errorf("Watchdog.Echo: got %v want %v", gw.Echo, ow.Echo)
	}

	// ToolLoop cap must survive (worker bounds its tool loop on this).
	if got.ToolLoop.MaxIterations != orig.ToolLoop.MaxIterations {
		t.Errorf("ToolLoop.MaxIterations: got %d want %d", got.ToolLoop.MaxIterations, orig.ToolLoop.MaxIterations)
	}

	// ModelProfiles must survive (worker resolves the reported cloud model from it).
	gmp, ok := got.ModelProfiles.Cloud.Providers["anthropic"]
	if !ok {
		t.Fatalf("ModelProfiles: anthropic vendor missing after round-trip; got %+v", got.ModelProfiles)
	}
	omp := orig.ModelProfiles.Cloud.Providers["anthropic"]
	if gmp.Economy.Model != omp.Economy.Model {
		t.Errorf("ModelProfiles anthropic.Economy: got %q want %q", gmp.Economy.Model, omp.Economy.Model)
	}
	if gmp.Standard.Model != omp.Standard.Model {
		t.Errorf("ModelProfiles anthropic.Standard: got %q want %q", gmp.Standard.Model, omp.Standard.Model)
	}
	if gmp.Premium.Model != omp.Premium.Model {
		t.Errorf("ModelProfiles anthropic.Premium: got %q want %q", gmp.Premium.Model, omp.Premium.Model)
	}
}
