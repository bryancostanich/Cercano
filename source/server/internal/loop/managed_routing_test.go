package loop_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
	"google.golang.org/adk/session"
)

type legacyRouter struct{ personal agent.TurnRunner }

func (r legacyRouter) ClassifyIntent(*agent.Request) (agent.Intent, error) {
	panic("managed request classified by personal router")
}
func (r legacyRouter) SelectProvider(*agent.Request, agent.Intent) (agent.TurnRunner, error) {
	panic("managed request used personal router")
}
func (r legacyRouter) Tiers() agent.Tiers { return agent.Tiers{Open: r.personal, Cloud: r.personal} }

type legacyManagedProvider struct {
	calls []llm.ChatRequest
	fail  bool
}

func (*legacyManagedProvider) Name() string                         { return "openai" }
func (*legacyManagedProvider) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (p *legacyManagedProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.calls = append(p.calls, req)
	if p.fail {
		return llm.ChatResponse{}, modelpolicy.Deny(modelpolicy.Attempt{Model: req.Model}, "revoked")
	}
	return llm.ChatResponse{Blocks: []llm.Block{{Type: llm.BlockText, Text: "generated code"}}, Model: req.Model}, nil
}
func (*legacyManagedProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	panic("unexpected stream")
}

func TestManagedLegacyRequestsUseLockedDefaultsAndKeepFileValidation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, coding := range []bool{false, true} {
			name := "unary"
			if stream {
				name = "stream"
			}
			if coding {
				name += " coding"
			}
			t.Run(name, func(t *testing.T) {
				personal := &seqProvider{name: "personal", outputs: []string{"personal"}}
				approved := &legacyManagedProvider{}
				checks := 0
				val := &funcValidator{fn: func(context.Context, string) error {
					checks++
					if checks < 3 {
						return errors.New("retry validation")
					}
					return nil
				}}
				coord := loop.NewADKCoordinator(personal, personal, val, session.InMemoryService())
				a := agent.NewAgent(legacyRouter{personal}, coord)
				a.SetManagedCandidates(func() inference.Tiers {
					return inference.Tiers{ManagedRoute: func(context.Context, v1.Route, config.Destination) (inference.Candidate, error) {
						return inference.Candidate{Provider: approved, IsCloud: true}, nil
					}}
				})
				snap := settingstest.Snapshot("company-a", "1", "Review")
				ctx := managedsettings.WithSnapshot(context.Background(), snap)
				req := &agent.Request{Input: "use cloud and generate"}
				if coding {
					req.WorkDir = t.TempDir()
					req.FileName = "main.go"
					if err := os.WriteFile(filepath.Join(req.WorkDir, req.FileName), []byte("original"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				run := func() (*agent.Response, error) {
					if stream {
						return a.ProcessRequestStream(ctx, req, nil, nil)
					}
					return a.ProcessRequest(ctx, req)
				}
				result, err := run()
				if err != nil || personal.calls != 0 {
					t.Fatalf("personal route selected: %v calls=%d", err, personal.calls)
				}
				want := 1
				if coding {
					want = 4
				}
				if len(approved.calls) != want {
					t.Fatalf("calls=%d want=%d", len(approved.calls), want)
				}
				for _, call := range approved.calls {
					if call.Model != "approved" || call.Tier != "" {
						t.Fatalf("managed route was not bound to its exact physical model: %+v", call)
					}
				}
				if coding {
					original, err := os.ReadFile(filepath.Join(req.WorkDir, req.FileName))
					if err != nil || string(original) != "original" || len(result.FileChanges) != 1 {
						t.Fatal("file validation/restoration changed", err)
					}
				}
				// Explicit model choices must not bypass a locked administrator default.
				req.ModelOverride = "personal"
				if _, err = run(); !modelpolicy.IsDenial(err) || len(approved.calls) != want {
					t.Fatal("locked override ran", err)
				}
				req.ModelOverride = ""
				approved.fail = true
				if _, err = run(); !modelpolicy.IsDenial(err) || personal.calls != 0 {
					t.Fatal("denial degraded to personal provider", err)
				}
			})
		}
	}
}

func TestManagedLegacyDirectOpenAndMissingBindingsFailClosed(t *testing.T) {
	personal := &seqProvider{name: "personal", outputs: []string{"personal"}}
	a := agent.NewAgent(legacyRouter{personal}, nil)
	snapshot := settingstest.Snapshot("company-a", "1", "Review")
	snapshot.Policy.TaskDefaults[0].Task = string(config.TaskDispatch)
	ctx := managedsettings.WithSnapshot(context.Background(), snapshot)
	if _, err := a.ProcessRequest(ctx, &agent.Request{Input: "hi"}); !modelpolicy.IsDenial(err) {
		t.Fatal(err)
	}
	a.SetManagedCandidates(func() inference.Tiers { return inference.Tiers{} })
	if _, err := a.ProcessRequest(ctx, &agent.Request{Input: "hi", DirectOpen: true}); !modelpolicy.IsDenial(err) || personal.calls != 0 {
		t.Fatal("local request used external default", err)
	}
	coord := loop.NewADKCoordinator(personal, personal, nil, session.InMemoryService())
	if _, err := coord.Coordinate(ctx, "hi", "", t.TempDir(), "main.go", nil); !modelpolicy.IsDenial(err) || personal.calls != 0 {
		t.Fatal("coordinator used unpinned settings", err)
	}
}
