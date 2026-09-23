package profilechain

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

type cycleProbe struct {
	mu           sync.Mutex
	calls        int
	failure      error
	partial      bool
	dial         bool
	eventFailure bool
}

func (*cycleProbe) Name() string { return "same-vendor" }
func (*cycleProbe) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *cycleProbe) Chat(context.Context, inference.Call) (inference.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return inference.Result{}, p.failure
}
func (p *cycleProbe) StreamChat(context.Context, inference.Call) (inference.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.dial {
		return nil, p.failure
	}
	return &cycleProbeStream{failure: p.failure, partial: p.partial, eventFailure: p.eventFailure}, nil
}
func (p *cycleProbe) set(err error) { p.mu.Lock(); defer p.mu.Unlock(); p.failure = err }
func (p *cycleProbe) count() int    { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }

type cycleProbeStream struct {
	eventFailure bool
	failure      error
	partial      bool
	step         int
}

func (s *cycleProbeStream) Next() (llm.StreamEvent, bool, error) {
	s.step++
	if s.partial && s.step == 1 {
		return llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: "already emitted"}, true, nil
	}
	if s.failure != nil {
		if s.eventFailure {
			return llm.StreamEvent{Type: llm.EventError, Err: s.failure}, true, nil
		}
		return llm.StreamEvent{}, false, s.failure
	}
	return llm.StreamEvent{}, false, nil
}
func (*cycleProbeStream) Close() error { return nil }
func cycleFixture(t *testing.T) (config.Config, map[string]*cycleProbe, Builder) {
	t.Helper()
	c := config.Defaults()
	c.ActiveCloudProfile = "a"
	c.SetPrimaryBackups([]string{"b", "c"})
	probes := map[string]*cycleProbe{}
	for _, name := range []string{"a", "b", "c"} {
		c.CloudProfiles = append(c.CloudProfiles, config.CloudProfile{Name: name, Provider: "deepinfra"})
		probes[name] = &cycleProbe{}
	}
	return c, probes, func(p config.CloudProfile) (inference.Provider, error) { return probes[p.Name], nil }
}
func quotaError() error {
	return &llm.Error{Class: llm.ErrQuota, Provider: "same-vendor", Err: errors.New("quota")}
}
func TestCycleStickyWraparoundAndExhaustion(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "stream"}[streaming], func(t *testing.T) {
			c, probes, build := cycleFixture(t)
			p, err := Build(c, config.DestinationPrimary, build)
			if err != nil {
				t.Fatal(err)
			}
			call := func() error {
				req := inference.Call{Tier: string(config.TierEveryday)}
				if !streaming {
					_, err := p.Chat(context.Background(), req)
					return err
				}
				s, err := p.StreamChat(context.Background(), req)
				if err != nil {
					return err
				}
				defer s.Close()
				for i := 0; i < 30; i++ {
					_, ok, err := s.Next()
					if err != nil {
						return err
					}
					if !ok {
						return nil
					}
				}
				t.Fatal("unbounded stream")
				return nil
			}
			probes["a"].set(quotaError())
			if err := call(); err != nil {
				t.Fatal(err)
			}
			if err := call(); err != nil {
				t.Fatal(err)
			}
			if probes["a"].count() != 1 || probes["b"].count() != 2 || probes["c"].count() != 0 {
				t.Fatal("did not stay on b")
			}
			probes["b"].set(quotaError())
			if err := call(); err != nil {
				t.Fatal(err)
			}
			if probes["c"].count() != 1 {
				t.Fatal("did not advance to c")
			}
			probes["a"].set(nil)
			probes["c"].set(quotaError())
			if err := call(); err != nil {
				t.Fatal(err)
			}
			if got := inference.TargetForCall(p, inference.Call{Tier: string(config.TierEveryday)}).Profile; got != "a" {
				t.Fatal("did not wrap to recovered a", got)
			}
			probes["a"].set(quotaError())
			before := probes["a"].count() + probes["b"].count() + probes["c"].count()
			if err := call(); err == nil || !strings.Contains(err.Error(), "all configured cloud accounts exhausted") {
				t.Fatalf("exhaustion: %v", err)
			}
			after := probes["a"].count() + probes["b"].count() + probes["c"].count()
			if after-before != 3 {
				t.Fatalf("attempted %d accounts, want 3", after-before)
			}
		})
	}
}
func TestCyclePreservesSelectionAcrossRebuild(t *testing.T) {
	c, probes, build := cycleFixture(t)
	p, err := Build(c, config.DestinationPrimary, build)
	if err != nil {
		t.Fatal(err)
	}
	probes["a"].set(quotaError())
	if _, err := p.Chat(context.Background(), inference.Call{Tier: string(config.TierEveryday)}); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := BuildPrimary(c, build, StateOf(p))
	if err != nil {
		t.Fatal(err)
	}
	if got := inference.TargetForCall(rebuilt, inference.Call{Tier: string(config.TierEveryday)}).Profile; got != "b" {
		t.Fatal(got)
	}
	c.SetPrimaryBackups([]string{"c"})
	rebuilt, err = BuildPrimary(c, build, StateOf(rebuilt))
	if err != nil {
		t.Fatal(err)
	}
	if got := inference.TargetForCall(rebuilt, inference.Call{Tier: string(config.TierEveryday)}).Profile; got != "a" {
		t.Fatal("removed active account retained", got)
	}
}
func TestCycleDoesNotReplayPartialStream(t *testing.T) {
	c, probes, build := cycleFixture(t)
	probes["a"].partial = true
	probes["a"].set(quotaError())
	p, err := Build(c, config.DestinationPrimary, build)
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.StreamChat(context.Background(), inference.Call{Tier: string(config.TierEveryday)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ev, ok, err := s.Next(); err != nil || !ok || ev.Type != llm.EventTextDelta {
		t.Fatalf("first: %+v %v", ev, err)
	}
	if _, _, err := s.Next(); llm.ClassOf(err) != llm.ErrQuota {
		t.Fatal(err)
	}
	if probes["b"].count() != 0 {
		t.Fatal("replayed partial stream")
	}
	if got := inference.TargetForCall(p, inference.Call{Tier: string(config.TierEveryday)}).Profile; got != "b" {
		t.Fatal("next request not advanced", got)
	}
}
func TestCycleConcurrentQuotaFailures(t *testing.T) {
	c, probes, build := cycleFixture(t)
	probes["a"].set(quotaError())
	p, err := Build(c, config.DestinationPrimary, build)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Chat(context.Background(), inference.Call{Tier: string(config.TierEveryday)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := inference.TargetForCall(p, inference.Call{Tier: string(config.TierEveryday)}).Profile; got != "b" {
		t.Fatal("concurrent failures skipped healthy account", got)
	}
}

func TestCycleSkipsBackupWithoutRequestedModel(t *testing.T) {
	c, probes, build := cycleFixture(t)
	c.CloudProfiles[1].Provider = "unknown-no-catalog"
	probes["a"].set(quotaError())
	p, err := Build(c, config.DestinationPrimary, build)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Chat(context.Background(), inference.Call{Tier: string(config.TierEveryday)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Route == nil || result.Route.Profile != "c" || probes["b"].count() != 0 {
		t.Fatalf("wrong compatible backup: %+v", result.Route)
	}
}

func TestCycleStreamDialAndErrorEventExhaustion(t *testing.T) {
	for _, dial := range []bool{false, true} {
		c, probes, build := cycleFixture(t)
		for _, p := range probes {
			p.set(quotaError())
			p.dial = dial
			p.eventFailure = !dial
		}
		p, err := Build(c, config.DestinationPrimary, build)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := p.StreamChat(context.Background(), inference.Call{Tier: string(config.TierEveryday)})
		if stream != nil {
			defer stream.Close()
			for i := 0; i < 20 && err == nil; i++ {
				var ev llm.StreamEvent
				var ok bool
				ev, ok, err = stream.Next()
				if ev.Type == llm.EventError {
					err = ev.Err
				}
				if !ok {
					break
				}
			}
		}
		if err == nil || !strings.Contains(err.Error(), "all configured cloud accounts exhausted") {
			t.Fatalf("dial=%v: %v", dial, err)
		}
		for name, p := range probes {
			if p.count() != 1 {
				t.Fatalf("dial=%v %s calls=%d", dial, name, p.count())
			}
		}
	}
}
func TestCycleOldGraphCannotAdvanceRebuiltSelection(t *testing.T) {
	c, probes, build := cycleFixture(t)
	probes["a"].set(quotaError())
	old, err := Build(c, config.DestinationPrimary, build)
	if err != nil {
		t.Fatal(err)
	}
	c.SetPrimaryBackups([]string{"c"})
	current, err := BuildPrimary(c, build, StateOf(old))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = old.Chat(context.Background(), inference.Call{Tier: string(config.TierEveryday)})
	if got := inference.TargetForCall(current, inference.Call{Tier: string(config.TierEveryday)}).Profile; got != "a" {
		t.Fatal("old graph moved current selection", got)
	}
}
