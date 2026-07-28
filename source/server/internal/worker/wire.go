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
	"log"

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
		Role:       string(m.Role),
		BlocksJson: blocksJSON,
		Content:    text,
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
		Kind:         eventKindToProto(ev.Kind),
		Text:         ev.Text,
		Model:        ev.Model,
		IsCloud:      ev.IsCloud,
		ToolUseId:    ev.ToolUseID,
		ToolName:     ev.ToolName,
		ArgsSummary:  ev.ArgsSummary,
		Detail:       ev.Detail,
		Summary:      ev.Summary,
		StartLine:    int32(ev.StartLine),
		IsError:      ev.IsError,
		WatchdogKind: ev.WatchdogKind,
		Thread:       ev.Thread,
		Notice:       ev.Notice,

		SubAgentId:       ev.SubAgentID,
		SubAgentParentId: ev.SubAgentParentID,
		SubAgentTitle:    ev.SubAgentTitle,
		SubAgentKind:     ev.SubAgentKind,
		GrantedTools:     ev.GrantedTools,
		IgnoredTools:     ev.IgnoredTools,
	}
	// For EventDone, inline the Result fields.
	if ev.Kind == runner.EventDone {
		p.FinalText = ev.Result.FinalText
		p.Model = ev.Result.Model
		p.IsCloud = ev.Result.IsCloud
		p.InputTokens = int64(ev.Result.InputTokens)
		p.OutputTokens = int64(ev.Result.OutputTokens)
		p.Notice = ev.Result.Notice
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

		SubAgentID:       p.SubAgentId,
		SubAgentParentID: p.SubAgentParentId,
		SubAgentTitle:    p.SubAgentTitle,
		SubAgentKind:     p.SubAgentKind,
		GrantedTools:     p.GrantedTools,
		IgnoredTools:     p.IgnoredTools,
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
	case runner.EventSubAgent:
		return proto.WorkerEventKind_WORKER_EVENT_KIND_SUBAGENT
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
	case proto.WorkerEventKind_WORKER_EVENT_KIND_SUBAGENT:
		return runner.EventSubAgent
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
		flavor     string
		backend    string
		route      string
		baseURL    string
		model      string
		region     string
		awsProfile string
	)
	for _, p := range cfg.CloudProfiles {
		if p.Name == cfg.ActiveCloudProfile {
			flavor = p.Flavor
			backend = p.Backend
			route = p.Route
			baseURL = p.BaseURL
			model = p.Model
			region = p.Region
			awsProfile = p.AWSProfile
			break
		}
	}

	// Pull backup cloud profile fields (mirror the active-profile fields) so the
	// worker can build the backup provider and wrap active+backup in a fallback
	// composite, matching in-process behavior. Only populated when a distinct
	// backup profile is configured; its credential is fetched via the same stream
	// credential proxy in the worker, keyed by the backup profile name.
	var (
		backupFlavor     string
		backupBackend    string
		backupRoute      string
		backupBaseURL    string
		backupModel      string
		backupRegion     string
		backupAWSProfile string
	)
	if cfg.BackupCloudProfile != "" && cfg.BackupCloudProfile != cfg.ActiveCloudProfile {
		for _, p := range cfg.CloudProfiles {
			if p.Name == cfg.BackupCloudProfile {
				backupFlavor = p.Flavor
				backupBackend = p.Backend
				backupRoute = p.Route
				backupBaseURL = p.BaseURL
				backupModel = p.Model
				backupRegion = p.Region
				backupAWSProfile = p.AWSProfile
				break
			}
		}
	}

	// Carry ModelProfiles as a JSON blob (nested struct; not mirrored in proto).
	var modelProfilesJSON string
	if b, err := json.Marshal(cfg.ModelProfiles); err != nil {
		log.Printf("[worker] snapshot: marshal ModelProfiles: %v", err)
	} else {
		modelProfilesJSON = string(b)
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
		BackupFlavor:       backupFlavor,
		BackupBackend:      backupBackend,
		BackupRoute:        backupRoute,
		BackupBaseUrl:      backupBaseURL,
		BackupModel:        backupModel,
		BackupRegion:       backupRegion,
		BackupAwsProfile:   backupAWSProfile,
		ResolvedCredential: cred,

		OllamaUrl:   cfg.OllamaURL,
		OpenRuntime: cfg.OpenRuntime,

		// Cloud tier slots are removed from the model taxonomy (cloud resolves
		// via the vendor-keyed cost-tier path); the proto still carries the
		// retired Tier*Cloud fields until they are dropped in the schema-regen
		// commit, so they are simply left unset here. DefaultProvider is still
		// carried until that same commit retires it.
		TierMostCapableOpen:   t.MostCapable.Open,
		TierEverydayOpen:      t.Everyday.Open,
		TierFastLightOpen:     t.FastLight.Open,
		TierFastLightTextOpen: t.FastLightText.Open,
		TierEmbeddingOpen:     t.Embedding.Open,
		DefaultProvider:       string(cfg.Models.DefaultProvider),

		CompactionEnabled:               cfg.Compaction.Enabled,
		CompactionActivationFloorTokens: int32(cfg.Compaction.ActivationFloorTokens),
		CompactionSegmentTokens:         int32(cfg.Compaction.SegmentTokens),
		CompactionVerbatimRecent:        int32(cfg.Compaction.VerbatimRecent),
		CompactionHardOverridePct:       cfg.Compaction.HardOverridePct,
		ElideToolResults:                cfg.Compaction.ElideToolResults,
		LossyToolElision:                cfg.Compaction.LossyToolElision,

		WatchdogEnabled:       cfg.Watchdog.Enabled,
		WatchdogMode:          cfg.Watchdog.Mode,
		WatchdogChecks:        cfg.Watchdog.Checks,
		WatchdogModel:         cfg.Watchdog.Model,
		WatchdogEscalateAfter: int32(cfg.Watchdog.EscalateAfter),
		WatchdogEcho:          cfg.Watchdog.Echo,

		ToolLoopMaxIterations: int32(cfg.ToolLoop.MaxIterations),
		ModelProfilesJson:     modelProfilesJSON,
	}
}

// ConfigFromSnapshot reconstructs the config.Config fields the worker cares
// about from a ConfigSnapshot. Fields the worker never reads (e.g. Port,
// retention, LlamaServer) are left at zero/default.
func ConfigFromSnapshot(p *proto.ConfigSnapshot) config.Config {
	if p == nil {
		return config.Config{}
	}

	// Rebuild the active cloud profile FIRST — buildWorkerProviders relies on
	// CloudProfiles[0] being the active profile.
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
	// Rebuild the backup cloud profile as a second entry (when distinct from the
	// active profile) so the worker can build the fallback composite, matching
	// in-process wrapBackup.
	if p.BackupCloudProfile != "" && p.BackupCloudProfile != p.ActiveCloudProfile {
		profiles = append(profiles, config.CloudProfile{
			Name:       p.BackupCloudProfile,
			Flavor:     p.BackupFlavor,
			Backend:    p.BackupBackend,
			Route:      p.BackupRoute,
			BaseURL:    p.BackupBaseUrl,
			Model:      p.BackupModel,
			Region:     p.BackupRegion,
			AWSProfile: p.BackupAwsProfile,
		})
	}

	// Rebuild ModelProfiles from its JSON blob (defensive: zero on error).
	var mp config.ModelProfiles
	if p.ModelProfilesJson != "" {
		if err := json.Unmarshal([]byte(p.ModelProfilesJson), &mp); err != nil {
			log.Printf("[worker] snapshot: unmarshal ModelProfiles: %v", err)
			mp = config.ModelProfiles{}
		}
	}

	cfg := config.Config{
		LocusMode:          p.LocusMode,
		CloudProfiles:      profiles,
		ActiveCloudProfile: p.ActiveCloudProfile,
		BackupCloudProfile: p.BackupCloudProfile,
		OllamaURL:          p.OllamaUrl,
		OpenRuntime:        p.OpenRuntime,
		ModelProfiles:      mp,
		ToolLoop:           config.ToolLoopConfig{MaxIterations: int(p.ToolLoopMaxIterations)},

		Models: config.ModelsConfig{
			DefaultProvider: config.Provider(p.DefaultProvider),
			Tiers: config.ModelTiers{
				MostCapable:   config.ModelTier{Open: p.TierMostCapableOpen},
				Everyday:      config.ModelTier{Open: p.TierEverydayOpen},
				FastLight:     config.ModelTier{Open: p.TierFastLightOpen},
				FastLightText: config.ModelTier{Open: p.TierFastLightTextOpen},
				Embedding:     config.ModelTier{Open: p.TierEmbeddingOpen},
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
