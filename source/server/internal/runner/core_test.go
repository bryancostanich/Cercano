package runner

// TestCore_ConcurrentTurns_NoCrossTalk is the load-bearing concurrent
// cross-talk guard for runner.Core. It proves two Cores run concurrently in
// one process with different workdirs, conversation IDs, and session IDs and
// NEVER interfere — the invariant that makes both embedded mode and the Phase-5
// worker safe.
//
// Mutation-check note: to confirm the test bites, temporarily add a
// package-level `var lastWorkDir string` written by RunTurn and read by the
// provider instead of the ctx value — the test then fails under -race (cross-
// talk detected). Revert before committing. (Done during authorship; see task-3
// report.)

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	engine "cercano/source/server/internal/engine"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	permissions "cercano/source/server/internal/hostsvc/permissions"
	providers "cercano/source/server/internal/hostsvc/providers"
	legacymodels "cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/watchdog"
	"cercano/source/server/pkg/config"
)

// ---------------------------------------------------------------------------
// Observation type — the scripted provider records what the ctx carries.
// ---------------------------------------------------------------------------

type turnObservation struct {
	workDir   string
	sessionID string
}

// ---------------------------------------------------------------------------
// spyProvider — scripted llm.Provider that records ctx values and writes a
// marker file under the observed WorkDir. It emits a single end_turn stream so
// RunToolLoop exits after one iteration without invoking any real tools.
// ---------------------------------------------------------------------------

type spyProvider struct {
	mu           sync.Mutex
	observations []turnObservation
}

func (p *spyProvider) Name() string { return "spy" }

func (p *spyProvider) Capabilities() llm.Capabilities {
	return llm.Capabilities{SupportsTools: true}
}

func (p *spyProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{StopReason: "end_turn"}, nil
}

func (p *spyProvider) StreamChat(ctx context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	workDir := agenttools.WorkDirFromContext(ctx)
	sessionID := llm.SessionIDFromContext(ctx)

	p.mu.Lock()
	p.observations = append(p.observations, turnObservation{
		workDir:   workDir,
		sessionID: sessionID,
	})
	p.mu.Unlock()

	// Write a marker file under the observed WorkDir so we can assert
	// filesystem isolation (impossible if any global cwd/session were shared).
	if workDir != "" {
		_ = os.WriteFile(filepath.Join(workDir, "marker"), []byte(sessionID), 0o600)
	}

	// Return a stream that immediately signals end_turn with no tool calls.
	// RunToolLoop exits when it sees StopReason="end_turn" with no tool-use
	// blocks in the response — exactly one chat round, no tool dispatch.
	return &endTurnReader{}, nil
}

// endTurnReader yields a single message_stop / end_turn event then EOF.
type endTurnReader struct {
	done bool
}

func (r *endTurnReader) Next() (llm.StreamEvent, bool, error) {
	if r.done {
		return llm.StreamEvent{}, false, nil
	}
	r.done = true
	return llm.StreamEvent{
		Type:       llm.EventMessageStop,
		StopReason: "end_turn",
	}, true, nil
}

func (r *endTurnReader) Close() error { return nil }

// ---------------------------------------------------------------------------
// fakeResolver — minimal providers.Resolver returning the spy provider.
// Only Main() and MainModel() are called by RunTurn; the rest are stubs.
// ---------------------------------------------------------------------------

type fakeResolver struct{ prov llm.Provider }

func (f *fakeResolver) Main() (llm.Provider, bool, bool, error) {
	return f.prov, false, false, nil
}
func (f *fakeResolver) MainModel(_ bool) string                    { return "fake-model" }
func (f *fakeResolver) PrimaryModel() string                       { return "fake-model" }
func (f *fakeResolver) Rebuild() error                             { return nil }
func (f *fakeResolver) InstallAbsentCloud(_ string)                {}
func (f *fakeResolver) Cloud() llm.Provider                        { return nil }
func (f *fakeResolver) Open() llm.Provider                         { return nil }
func (f *fakeResolver) ActiveCloudModel() string                   { return "" }
func (f *fakeResolver) LocusMode() string                          { return "" }
func (f *fakeResolver) Router() providers.RouterCloudUpdater       { return nil }
func (f *fakeResolver) Registry() *engine.EngineRegistry           { return nil }
func (f *fakeResolver) CatalogManager() *ollamacatalog.Manager { return nil }
func (f *fakeResolver) OpenLegacy() *legacymodels.OpenModelProvider { return nil }
func (f *fakeResolver) Reconfigure(_ providers.ReconfigureArgs)    {}
func (f *fakeResolver) SetCloudLLMProvider(_ llm.Provider)         {}
func (f *fakeResolver) SetOpenLLMProvider(_ llm.Provider)          {}
func (f *fakeResolver) SetOpenProviderFactory(_ func(config.Config) llm.Provider) {}
func (f *fakeResolver) CloudLLMProvider() llm.Provider             { return nil }
func (f *fakeResolver) OpenLLMProvider() llm.Provider              { return nil }
func (f *fakeResolver) SetCatalogManager(_ *ollamacatalog.Manager)  {}
func (f *fakeResolver) SetUsageSink(_ func(usage.Usage))           {}

// ---------------------------------------------------------------------------
// fakeTurnHistory — minimal TurnHistory; returns empty history.
// ---------------------------------------------------------------------------

type fakeTurnHistory struct{}

func (h *fakeTurnHistory) AssembleHistory(_ context.Context, _ string) []llm.Message {
	return nil
}
func (h *fakeTurnHistory) PersistTurn(_ context.Context, _ string, _ llm.Message) {}
func (h *fakeTurnHistory) LoadProjectContext(_ string) string                       { return "" }

// ---------------------------------------------------------------------------
// fakeToolSvc — minimal ToolSvc; returns an empty registry.
// ---------------------------------------------------------------------------

type fakeToolSvc struct{ reg *agenttools.Registry }

func (s *fakeToolSvc) Registry() *agenttools.Registry { return s.reg }
func (s *fakeToolSvc) GrantedRegistry(_ []string) (*agenttools.Registry, []string, []string, error) {
	return s.reg, nil, nil, nil
}

// ---------------------------------------------------------------------------
// fakeConfig — minimal config.Service returning a zero Config.
// The runner only reads Config.Get() inside the watchdog and fallback blocks,
// which are not exercised here (Watchdog=nil, loop succeeds).
// ---------------------------------------------------------------------------

type fakeConfig struct{}

func (c *fakeConfig) Get() config.Config                                           { return config.Config{} }
func (c *fakeConfig) Path() string                                                 { return "" }
func (c *fakeConfig) Secrets() secrets.Store                                       { return nil }
func (c *fakeConfig) ActiveProfile() (config.CloudProfile, bool)                   { return config.CloudProfile{}, false }
func (c *fakeConfig) Set(_ config.Config)                                           {}
func (c *fakeConfig) SetPath(_ string)                                             {}
func (c *fakeConfig) SetSecrets(_ secrets.Store)                                   {}
func (c *fakeConfig) SetActiveProfile(_ string) bool                               { return false }
func (c *fakeConfig) UpsertProfile(_ config.CloudProfile) (bool, bool)            { return false, false }
func (c *fakeConfig) RemoveProfile(_ string) (bool, bool)                          { return false, false }
func (c *fakeConfig) SetBackupProfile(_ string) bool                               { return false }
func (c *fakeConfig) ProfileInfo(_ string) (bool, bool)                            { return false, false }
func (c *fakeConfig) Mutate(_ func(*config.Config))                                {}
func (c *fakeConfig) SetCloudModel(_ string)                                       {}
func (c *fakeConfig) Persist()                                                     {}

// ---------------------------------------------------------------------------
// fakePerms — minimal permissions.Broker returning a permissive PermissionStore.
// ---------------------------------------------------------------------------

type fakePerms struct{ store *agent.PermissionStore }

func (p *fakePerms) Mode() agent.PermissionMode             { return agent.ModePermissive }
func (p *fakePerms) SetMode(_ agent.PermissionMode) error   { return nil }
func (p *fakePerms) AddMCPAllow(_ string) error             { return nil }
func (p *fakePerms) HasPending() bool                       { return false }
func (p *fakePerms) Wait(_ context.Context, _, _ string) (agent.Decision, error) {
	return agent.Decision{}, nil
}
func (p *fakePerms) Resolve(_, _ string, _ agent.Decision) bool { return true }
func (p *fakePerms) Store() *agent.PermissionStore           { return p.store }
func (p *fakePerms) StartWatcher(_ context.Context, _ string) error { return nil }

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks.
// These fail to compile if any fake diverges from the real interface.
// ---------------------------------------------------------------------------

var _ providers.Resolver    = (*fakeResolver)(nil)
var _ cfgsvc.Service        = (*fakeConfig)(nil)
var _ permissions.Broker    = (*fakePerms)(nil)
var _ TurnHistory            = (*fakeTurnHistory)(nil)
var _ ToolSvc                = (*fakeToolSvc)(nil)

// ---------------------------------------------------------------------------
// noopSink discards all runner events (we only care about spy observations).
// ---------------------------------------------------------------------------

type noopSink struct{}

func (noopSink) Emit(_ Event) {}

// ---------------------------------------------------------------------------
// buildDeps constructs a Deps wired with a given spy provider.
// ---------------------------------------------------------------------------

func buildDeps(spy llm.Provider) Deps {
	reg := agenttools.NewRegistry()
	return Deps{
		Providers: &fakeResolver{prov: spy},
		Tools:     &fakeToolSvc{reg: reg},
		Persist:   &fakeTurnHistory{},
		Config:    &fakeConfig{},
		Perms:     &fakePerms{store: agent.NewStaticPermissionStore(agent.ModePermissive)},
		Agent:     nil, // nil-safe; RecordContextUsage handles nil receiver
		Watchdog:  nil,
	}
}

// TestCore_WatchdogReadLiveAtTurnTime is the C1 regression guard.
//
// The host builds the runner (SetPermissions) BEFORE it wires its watchdog
// (InitWatchdog) and it mutates the watchdog again on UpdateConfig — both
// AFTER construction. So Deps.Watchdog must be read live at turn time via the
// accessor, never snapshotted at New(). A snapshot would leave the watchdog
// permanently nil (silently disabled) for every user who enabled it — the
// exact regression the whole-branch review caught. This test fails if the
// runner ever reverts to reading a captured value instead of the accessor.
func TestCore_WatchdogReadLiveAtTurnTime(t *testing.T) {
	deps := buildDeps(&spyProvider{})

	// Accessor wired to a watchdog that does not exist yet at New() time and is
	// only "assigned" (observed via the call) when RunTurn asks for it.
	called := false
	deps.Watchdog = func() *watchdog.Watchdog {
		called = true
		return nil // disabled; we assert only that the runner consults it live
	}

	core := New(deps)
	// Ignore any read New() may have done; C1 is specifically about reading at
	// TURN time. Reset, then require RunTurn itself to consult the accessor.
	called = false
	if _, err := core.RunTurn(context.Background(), Request{
		ConversationID: "conv-wd",
		Input:          "hi",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, noopSink{}, nil, nil); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	if !called {
		t.Fatal("runner did not read Deps.Watchdog via the accessor at turn time " +
			"(C1 regression: a construction-time snapshot leaves the watchdog permanently nil)")
	}
}

// ---------------------------------------------------------------------------
// TestCore_ConcurrentTurns_NoCrossTalk — THE LOAD-BEARING GUARD.
//
// Two independent Cores run concurrently (goroutines + WaitGroup) with:
//   - turn A: WorkDir=tmpA, ConversationID="conv-A"
//   - turn B: WorkDir=tmpB, ConversationID="conv-B"
//
// After both complete we assert:
//   (a) turn A's spy observed WorkDir=tmpA and sessionID containing "conv-A";
//       turn B observed tmpB / "conv-B" — no cross-read.
//   (b) tmpA contains only A's marker; tmpB contains only B's marker —
//       impossible if any process-global cwd/session state were shared.
//
// Run under -race (go test -race ./internal/runner/ -run TestCore_...) to
// exercise interleavings. Loop 20× to increase interleaving coverage.
// ---------------------------------------------------------------------------

func TestCore_ConcurrentTurns_NoCrossTalk(t *testing.T) {
	const iterations = 20

	for iter := 0; iter < iterations; iter++ {
		tmpA := t.TempDir()
		tmpB := t.TempDir()

		spyA := &spyProvider{}
		spyB := &spyProvider{}

		coreA := New(buildDeps(spyA))
		coreB := New(buildDeps(spyB))

		reqA := Request{
			ConversationID: "conv-A",
			Input:          "hello from A",
			WorkDir:        tmpA,
			Gen:            1,
		}
		reqB := Request{
			ConversationID: "conv-B",
			Input:          "hello from B",
			WorkDir:        tmpB,
			Gen:            1,
		}

		var wg sync.WaitGroup
		var errA, errB error

		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errA = coreA.RunTurn(context.Background(), reqA, noopSink{}, nil, nil)
		}()
		go func() {
			defer wg.Done()
			_, errB = coreB.RunTurn(context.Background(), reqB, noopSink{}, nil, nil)
		}()
		wg.Wait()

		if errA != nil {
			t.Fatalf("iter %d: turn A failed: %v", iter, errA)
		}
		if errB != nil {
			t.Fatalf("iter %d: turn B failed: %v", iter, errB)
		}

		// --- (a) Context observation: each turn saw its own WorkDir + session.

		if len(spyA.observations) == 0 {
			t.Fatalf("iter %d: turn A made no provider calls", iter)
		}
		if len(spyB.observations) == 0 {
			t.Fatalf("iter %d: turn B made no provider calls", iter)
		}

		for i, obs := range spyA.observations {
			if obs.workDir != tmpA {
				t.Errorf("iter %d: turn A call[%d]: workDir=%q, want %q (cross-talk!)", iter, i, obs.workDir, tmpA)
			}
			if obs.sessionID != "conv-A" {
				t.Errorf("iter %d: turn A call[%d]: sessionID=%q, want %q (cross-talk!)", iter, i, obs.sessionID, "conv-A")
			}
		}
		for i, obs := range spyB.observations {
			if obs.workDir != tmpB {
				t.Errorf("iter %d: turn B call[%d]: workDir=%q, want %q (cross-talk!)", iter, i, obs.workDir, tmpB)
			}
			if obs.sessionID != "conv-B" {
				t.Errorf("iter %d: turn B call[%d]: sessionID=%q, want %q (cross-talk!)", iter, i, obs.sessionID, "conv-B")
			}
		}

		// --- (b) Filesystem isolation: each marker is under its own tempdir.

		markerA, errReadA := os.ReadFile(filepath.Join(tmpA, "marker"))
		if errReadA != nil {
			t.Fatalf("iter %d: turn A marker missing: %v", iter, errReadA)
		}
		markerB, errReadB := os.ReadFile(filepath.Join(tmpB, "marker"))
		if errReadB != nil {
			t.Fatalf("iter %d: turn B marker missing: %v", iter, errReadB)
		}

		// Each marker contains the session ID written by that turn's provider.
		if string(markerA) != "conv-A" {
			t.Errorf("iter %d: markerA=%q, want %q (cross-talk!)", iter, markerA, "conv-A")
		}
		if string(markerB) != "conv-B" {
			t.Errorf("iter %d: markerB=%q, want %q (cross-talk!)", iter, markerB, "conv-B")
		}

		// The OTHER tempdir must NOT have a stray marker from the wrong turn.
		if _, err := os.Stat(filepath.Join(tmpB, "marker")); err == nil {
			// We already checked markerB above; confirm it holds the right value
			// (not the A session leaking in).
			if string(markerB) == "conv-A" {
				t.Errorf("iter %d: turn A's session ID leaked into tmpB (cross-talk!)", iter)
			}
		}
		if _, err := os.Stat(filepath.Join(tmpA, "marker")); err == nil {
			if string(markerA) == "conv-B" {
				t.Errorf("iter %d: turn B's session ID leaked into tmpA (cross-talk!)", iter)
			}
		}
	}
}

// Ensure secrets and usage are imported (used by fake stubs above).
var _ secrets.Store = nil
var _ usage.Usage = usage.Usage{}
