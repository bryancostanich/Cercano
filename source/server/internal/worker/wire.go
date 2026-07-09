// Package worker contains the wire codecs for the worker-process turn
// execution transport (Phase 5). This file provides Marshal/Unmarshal helpers
// between the in-process types (llm.Message, runner.Event, config.Config) and
// the proto envelope messages (LLMMessage, WorkerEvent, ConfigSnapshot).
//
// Serialization strategy for llm.Message:
//
//	The persistence layer already serializes llm.Message losslessly via
//	json.Marshal(m.Blocks). MarshalMessage reuses that exact encoding in
//	LLMMessage.BlocksJson, so round-trip fidelity is identical to what the
//	conversation store already guarantees. No fragile proto mirror of every
//	Block kind is needed; the JSON schema IS the contract.
package worker

import (
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"
)

// ─── LLMMessage ──────────────────────────────────────────────────────────────

// MarshalMessage converts an llm.Message to its proto wire form.
// blocks_json = json.Marshal(m.Blocks); content = concatenated text.
// This mirrors exactly what persistence.PersistTurn writes to the store.
func MarshalMessage(m llm.Message) (*proto.LLMMessage, error) {
	blocksJSON, err := json.Marshal(m.Blocks)
	if err != nil {
		return nil, fmt.Errorf("worker/wire: marshal blocks: %w", err)
	}
	var text string
	for _, b := range m.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	return &proto.LLMMessage{
		Role:      string(m.Role),
		BlocksJson: blocksJSON,
		Content:   text,
	}, nil
}

// UnmarshalMessage converts an LLMMessage proto back to llm.Message.
// It reconstructs Blocks by unmarshalling BlocksJson; Content is ignored
// (it is informational only, the blocks are authoritative).
func UnmarshalMessage(p *proto.LLMMessage) (llm.Message, error) {
	if p == nil {
		return llm.Message{}, fmt.Errorf("worker/wire: nil LLMMessage")
	}
	var blocks []llm.Block
	if len(p.BlocksJson) > 0 {
		if err := json.Unmarshal(p.BlocksJson, &blocks); err != nil {
			return llm.Message{}, fmt.Errorf("worker/wire: unmarshal blocks: %w", err)
		}
	}
	return llm.Message{
		Role:   llm.Role(p.Role),
		Blocks: blocks,
	}, nil
}

// ─── WorkerEvent ─────────────────────────────────────────────────────────────

// MarshalEvent converts a runner.Event to its proto wire form.
// The mapping is the inverse of server.sendRunnerEvent, keeping all fields
// so the host can reconstruct a runner.Event with zero information loss.
func MarshalEvent(ev runner.Event) *proto.WorkerEvent {
	p := &proto.WorkerEvent{
		Kind:        eventKindToProto(ev.Kind),
		Text:        ev.Text,
		Model:       ev.Model,
		IsCloud:     ev.IsCloud,
		ToolUseId:   ev.ToolUseID,
		ToolName:    ev.ToolName,
		ArgsSummary: ev.ArgsSummary,
		Detail:      ev.Detail,
		Summary:     ev.Summary,
		StartLine:   int32(ev.StartLine),
		IsError:     ev.IsError,
		WatchdogKind: ev.WatchdogKind,
		Thread:      ev.Thread,
		Notice:      ev.Notice,
	}
	// For EventDone, inline the Result fields.
	if ev.Kind == runner.EventDone {
		p.FinalText   = ev.Result.FinalText
		p.Model       = ev.Result.Model
		p.IsCloud     = ev.Result.IsCloud
		p.InputTokens  = int64(ev.Result.InputTokens)
		p.OutputTokens = int64(ev.Result.OutputTokens)
		p.Notice      = ev.Result.Notice
	}
	return p
}

// UnmarshalEvent converts a WorkerEvent proto back to runner.Event.
func UnmarshalEvent(p *proto.WorkerEvent) runner.Event {
	if p == nil {
		return runner.Event{}
	}
	kind := protoToEventKind(p.Kind)
	ev := runner.Event{
		Kind:         kind,
		Text:         p.Text,
		Model:        p.Model,
		IsCloud:      p.IsCloud,
		ToolUseID:    p.ToolUseId,
		ToolName:     p.ToolName,
		ArgsSummary:  p.ArgsSummary,
		Detail:       p.Detail,
		Summary:      p.Summary,
		StartLine:    int(p.StartLine),
		IsError:      p.IsError,
		WatchdogKind: p.WatchdogKind,
		Thread:       p.Thread,
		Notice:       p.Notice,
	}
	if kind == runner.EventDone {
		ev.Result = runner.Result{
			FinalText:    p.FinalText,
			Model:        p.Model,
			IsCloud:      p.IsCloud,
			InputTokens:  int(p.InputTokens),
			OutputTokens: int(p.OutputTokens),
			Notice:       p.Notice,
		}
	}
	return ev
}

func eventKindToProto(k runner.EventKind) proto.WorkerEventKind {
	switch k {
	case runner.EventRouteSelected:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_ROUTE_SELECTED
	case runner.EventToken:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_TOKEN
	case runner.EventProgress:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_PROGRESS
	case runner.EventToolUseStart:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_TOOL_USE_START
	case runner.EventToolUseStop:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_TOOL_USE_STOP
	case runner.EventToolExecStart:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_TOOL_EXEC_START
	case runner.EventToolExecComplete:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_TOOL_EXEC_COMPLETE
	case runner.EventWatchdog:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_WATCHDOG
	case runner.EventDone:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_DONE
	default:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_UNSPECIFIED
	}
}

func protoToEventKind(k proto.WorkerEventKind) runner.EventKind {
	switch k {
	case proto.WorkerEventKind_WORKER_EVENT_KIND_ROUTE_SELECTED:
		return runner.EventRouteSelected
	case proto.WorkerEventKind_WORKER_EVENT_KIND_TOKEN:
		return runner.EventToken
	case proto.WorkerEventKind_WORKER_EVENT_KIND_PROGRESS:
		return runner.EventProgress
	case proto.WorkerEventKind_WORKER_EVENT_KIND_TOOL_USE_START:
		return runner.EventToolUseStart
	case proto.WorkerEventKind_WORKER_EVENT_KIND_TOOL_USE_STOP:
		return runner.EventToolUseStop
	case proto.WorkerEventKind_WORKER_EVENT_KIND_TOOL_EXEC_START:
		return runner.EventToolExecStart
	case proto.WorkerEventKind_WORKER_EVENT_KIND_TOOL_EXEC_COMPLETE:
		return runner.EventToolExecComplete
	case proto.WorkerEventKind_WORKER_EVENT_KIND_WATCHDOG:
		return runner.EventWatchdog
	case proto.WorkerEventKind_WORKER_EVENT_KIND_DONE:
		return runner.EventDone
	default:
		return runner.EventKind(-1)
	}
}

// ─── ConfigSnapshot ───────────────────────────────────────────────────────────

// SnapshotConfig builds a ConfigSnapshot from a config.Config and the
// keychain-resolved API credential. The snapshot carries only the fields the
// worker's execution Deps need; nothing else crosses the process boundary.
func SnapshotConfig(cfg config.Config, cred string) *proto.ConfigSnapshot {
	// Pull active cloud profile fields.
	var (
		flavor      string
		backend     string
		route       string
		baseURL     string
		model       string
		region      string
		awsProfile  string
	)
	for _, p := range cfg.CloudProfiles {
		if p.Name == cfg.ActiveCloudProfile {
			flavor     = p.Flavor
			backend    = p.Backend
			route      = p.Route
			baseURL    = p.BaseURL
			model      = p.Model
			region     = p.Region
			awsProfile = p.AWSProfile
			break
		}
	}

	t := cfg.Models.Tiers
	return &proto.ConfigSnapshot{
		LocusMode:          cfg.LocusMode,
		ActiveCloudProfile: cfg.ActiveCloudProfile,
		CloudFlavor:        flavor,
		CloudBackend:       backend,
		CloudRoute:         route,
		CloudBaseUrl:       baseURL,
		CloudModel:         model,
		CloudRegion:        region,
		CloudAwsProfile:    awsProfile,
		BackupCloudProfile: cfg.BackupCloudProfile,
		ResolvedCredential: cred,

		OllamaUrl:   cfg.OllamaURL,
		OpenRuntime: cfg.OpenRuntime,

		TierMostCapableOpen:   t.MostCapable.Open,
		TierMostCapableCloud:  t.MostCapable.Cloud,
		TierEverydayOpen:      t.Everyday.Open,
		TierEverydayCloud:     t.Everyday.Cloud,
		TierFastLightOpen:     t.FastLight.Open,
		TierFastLightCloud:    t.FastLight.Cloud,
		TierFastLightTextOpen: t.FastLightText.Open,
		TierFastLightTextCloud: t.FastLightText.Cloud,
		TierEmbeddingOpen:     t.Embedding.Open,
		TierEmbeddingCloud:    t.Embedding.Cloud,
		DefaultProvider:       string(cfg.Models.DefaultProvider),

		CompactionEnabled:                cfg.Compaction.Enabled,
		CompactionActivationFloorTokens:  int32(cfg.Compaction.ActivationFloorTokens),
		CompactionSegmentTokens:          int32(cfg.Compaction.SegmentTokens),
		CompactionVerbatimRecent:         int32(cfg.Compaction.VerbatimRecent),
		CompactionHardOverridePct:        cfg.Compaction.HardOverridePct,
		ElideToolResults:                 cfg.Compaction.ElideToolResults,
		LossyToolElision:                 cfg.Compaction.LossyToolElision,

		WatchdogEnabled:       cfg.Watchdog.Enabled,
		WatchdogMode:          cfg.Watchdog.Mode,
		WatchdogChecks:        cfg.Watchdog.Checks,
		WatchdogModel:         cfg.Watchdog.Model,
		WatchdogEscalateAfter: int32(cfg.Watchdog.EscalateAfter),
		WatchdogEcho:          cfg.Watchdog.Echo,
	}
}

// ConfigFromSnapshot reconstructs the config.Config fields the worker cares
// about from a ConfigSnapshot. Fields the worker never reads (e.g. Port,
// retention, LlamaServer) are left at zero/default.
func ConfigFromSnapshot(p *proto.ConfigSnapshot) config.Config {
	if p == nil {
		return config.Config{}
	}

	// Rebuild the active cloud profile.
	profiles := []config.CloudProfile{}
	if p.ActiveCloudProfile != "" {
		profiles = append(profiles, config.CloudProfile{
			Name:       p.ActiveCloudProfile,
			Flavor:     p.CloudFlavor,
			Backend:    p.CloudBackend,
			Route:      p.CloudRoute,
			BaseURL:    p.CloudBaseUrl,
			Model:      p.CloudModel,
			Region:     p.CloudRegion,
			AWSProfile: p.CloudAwsProfile,
		})
	}

	cfg := config.Config{
		LocusMode:          p.LocusMode,
		CloudProfiles:      profiles,
		ActiveCloudProfile: p.ActiveCloudProfile,
		BackupCloudProfile: p.BackupCloudProfile,
		OllamaURL:          p.OllamaUrl,
		OpenRuntime:        p.OpenRuntime,

		Models: config.ModelsConfig{
			DefaultProvider: config.Provider(p.DefaultProvider),
			Tiers: config.ModelTiers{
				MostCapable:   config.ModelTier{Open: p.TierMostCapableOpen, Cloud: p.TierMostCapableCloud},
				Everyday:      config.ModelTier{Open: p.TierEverydayOpen, Cloud: p.TierEverydayCloud},
				FastLight:     config.ModelTier{Open: p.TierFastLightOpen, Cloud: p.TierFastLightCloud},
				FastLightText: config.ModelTier{Open: p.TierFastLightTextOpen, Cloud: p.TierFastLightTextCloud},
				Embedding:     config.ModelTier{Open: p.TierEmbeddingOpen, Cloud: p.TierEmbeddingCloud},
			},
		},

		Compaction: config.CompactionConfig{
			Enabled:               p.CompactionEnabled,
			ActivationFloorTokens: int(p.CompactionActivationFloorTokens),
			SegmentTokens:         int(p.CompactionSegmentTokens),
			VerbatimRecent:        int(p.CompactionVerbatimRecent),
			HardOverridePct:       p.CompactionHardOverridePct,
			ElideToolResults:      p.ElideToolResults,
			LossyToolElision:      p.LossyToolElision,
		},
		Watchdog: config.WatchdogConfig{
			Enabled:       p.WatchdogEnabled,
			Mode:          p.WatchdogMode,
			Checks:        p.WatchdogChecks,
			Model:         p.WatchdogModel,
			EscalateAfter: int(p.WatchdogEscalateAfter),
			Echo:          p.WatchdogEcho,
		},
	}
	return cfg
}
