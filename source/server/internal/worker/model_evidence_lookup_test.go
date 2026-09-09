package worker

import (
	"testing"

	"cercano/source/server/internal/modelmetadata"
	pkgcfg "cercano/source/server/pkg/config"
)

// cloudEvidenceFor mirrors the lookup buildDeps installs, so the resolution
// rules (active-then-backup, exact identity) are tested without standing up a
// full turn.
func cloudEvidenceFor(cfg pkgcfg.Config, snap modelmetadata.Snapshot, model string) modelmetadata.Evidence {
	if model == "" {
		return modelmetadata.Evidence{}
	}
	for _, name := range []string{cfg.ActiveCloudProfile, cfg.BackupCloudProfile} {
		if name == "" {
			continue
		}
		prof, ok := profileByName(cfg.CloudProfiles, name)
		if !ok {
			continue
		}
		if ev, found := snap.Lookup(modelmetadata.Identity{
			Provider: prof.Provider, BaseURL: prof.BaseURL, Route: prof.Route, Model: model,
		}); found {
			return ev
		}
	}
	return modelmetadata.Evidence{}
}

func twoProfileConfig() pkgcfg.Config {
	return pkgcfg.Config{
		ActiveCloudProfile: "primary",
		BackupCloudProfile: "backup",
		CloudProfiles: []pkgcfg.CloudProfile{
			{Name: "primary", Provider: "deepinfra", BaseURL: "https://api.deepinfra.com/v1/openai", Model: "big/model"},
			{Name: "backup", Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o"},
		},
	}
}

func TestWorkerEvidence_ResolvesBackupDestination(t *testing.T) {
	cfg := twoProfileConfig()
	snap := modelmetadata.Snapshot{
		{Identity: modelmetadata.Identity{Provider: "deepinfra", BaseURL: "https://api.deepinfra.com/v1/openai", Model: "big/model"},
			Evidence: modelmetadata.Evidence{ContextWindow: 1048576}},
		{Identity: modelmetadata.Identity{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o"},
			Evidence: modelmetadata.Evidence{ContextWindow: 128000, Vision: modelmetadata.VisionSupported}},
	}
	if ev := cloudEvidenceFor(cfg, snap, "big/model"); ev.ContextWindow != 1048576 {
		t.Fatalf("primary window = %d", ev.ContextWindow)
	}
	// The failover destination lives on the backup profile's endpoint. Without
	// searching it, this leg would silently fall back to the default window.
	ev := cloudEvidenceFor(cfg, snap, "gpt-4o")
	if ev.ContextWindow != 128000 || ev.Vision != modelmetadata.VisionSupported {
		t.Fatalf("backup evidence = %+v", ev)
	}
}

func TestWorkerEvidence_UnknownModelStaysUnknown(t *testing.T) {
	ev := cloudEvidenceFor(twoProfileConfig(), modelmetadata.Snapshot{}, "mystery/model")
	if ev.ContextWindow != 0 || ev.Vision != modelmetadata.VisionUnknown {
		t.Fatalf("got %+v, want zero evidence", ev)
	}
}

// Evidence recorded for one endpoint must not answer for another, even when
// the model id is identical.
func TestWorkerEvidence_DoesNotCrossEndpoints(t *testing.T) {
	cfg := twoProfileConfig()
	snap := modelmetadata.Snapshot{
		{Identity: modelmetadata.Identity{Provider: "somewhere-else", BaseURL: "https://other.example.com/v1", Model: "big/model"},
			Evidence: modelmetadata.Evidence{ContextWindow: 999999, Vision: modelmetadata.VisionSupported}},
	}
	if ev := cloudEvidenceFor(cfg, snap, "big/model"); ev.ContextWindow != 0 || ev.Vision != modelmetadata.VisionUnknown {
		t.Fatalf("evidence leaked across endpoints: %+v", ev)
	}
}

// Round-tripping through the wire must preserve an explicit unknown rather
// than dropping the entry.
func TestWorkerEvidence_SurvivesWireRoundTrip(t *testing.T) {
	in := modelmetadata.Snapshot{
		{Identity: modelmetadata.Identity{Provider: "deepinfra", BaseURL: "https://api.deepinfra.com/v1/openai", Route: "direct", Model: "a/b"},
			Evidence: modelmetadata.Evidence{ContextWindow: 262144, Vision: modelmetadata.VisionSupported}},
		{Identity: modelmetadata.Identity{Provider: "deepinfra", BaseURL: "https://api.deepinfra.com/v1/openai", Route: "direct", Model: "c/d"}},
	}
	out := UnmarshalModelMetadata(MarshalModelMetadata(in))
	if len(out) != len(in) {
		t.Fatalf("round trip lost entries: %d -> %d", len(in), len(out))
	}
	ev, ok := out.Lookup(in[1].Identity)
	if !ok {
		t.Fatal("explicit unknown entry dropped in transport")
	}
	if ev.Vision != modelmetadata.VisionUnknown || ev.ContextWindow != 0 {
		t.Fatalf("unknown entry mutated: %+v", ev)
	}
}
