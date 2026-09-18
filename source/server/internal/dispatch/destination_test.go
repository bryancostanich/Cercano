package dispatch

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
	"context"
	"errors"
	"testing"
)

type destinationProvider struct {
	echoProvider
	name  string
	calls []llm.ChatRequest
	fail  bool
}

func (p *destinationProvider) Name() string { return p.name }
func (p *destinationProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.calls = append(p.calls, req)
	if p.fail {
		return llm.ChatResponse{}, &llm.Error{Class: llm.ErrQuota, Err: errors.New("fixture quota")}
	}
	result, err := p.echoProvider.Chat(ctx, req)
	result.Model = req.Model
	return result, err
}

func TestDestinationChainsAndTaskTarget(t *testing.T) {
	for _, withPBackup := range []bool{false, true} {
		for _, withSBackup := range []bool{false, true} {
			p, pb, s, sb, local := &destinationProvider{name: "p", fail: true}, &destinationProvider{name: "pb"}, &destinationProvider{name: "s", fail: true}, &destinationProvider{name: "sb"}, &destinationProvider{name: "local"}
			chain := func(primary, backup *destinationProvider, enabled bool) inference.Provider {
				opts := resilience.Options{}
				if enabled {
					opts.Backup = backup
					opts.BackupModelFor = func(tier string) string { return backup.name + "-" + tier }
				}
				return resilience.New(primary, opts)
			}
			primary, secondary := chain(p, pb, withPBackup), chain(s, sb, withSBackup)
			candidates := Providers{Cloud: primary, Open: local, Destinations: map[config.Destination]inference.Candidate{
				config.DestinationPrimary: {Provider: primary, Profile: "p", IsCloud: true}, config.DestinationSecondary: {Provider: secondary, Profile: "s", IsCloud: true},
			}}
			e := NewEngine(provs(candidates), func() locus.Mode { return locus.CloudPrimary }, nil)
			e.SetDestinationModelFor(func(sel inference.Selection, tier config.Tier) string { return sel.Profile + "-" + string(tier) })
			target, err := e.Target(Spec{RoutingTask: config.TaskDispatch})
			if err != nil || target.Model != "s-most_capable" {
				t.Fatalf("target=%+v err=%v", target, err)
			}
			_, err = e.Dispatch(context.Background(), Spec{RoutingTask: config.TaskChat})
			if (err == nil) != withPBackup {
				t.Fatalf("primary backup=%t err=%v", withPBackup, err)
			}
			if len(s.calls)+len(sb.calls)+len(local.calls) != 0 {
				t.Fatal("Primary escaped chain")
			}
			_, err = e.Dispatch(context.Background(), Spec{RoutingTask: config.TaskDispatch})
			if (err == nil) != withSBackup {
				t.Fatalf("secondary backup=%t err=%v", withSBackup, err)
			}
			if len(p.calls) != 1 || len(s.calls) != 1 || len(local.calls) != 0 {
				t.Fatal("destination isolation failed")
			}
			// The engine reads built-in task defaults directly, so derive the
			// expected tier from the task rather than hardcoding one: this
			// asserts "backup inherits the task's quality" and stays correct
			// when a product default changes.
			chatTier := string((config.Config{}).TaskAssignment(config.TaskChat).Quality.CapabilityTier())
			dispatchTier := string((config.Config{}).TaskAssignment(config.TaskDispatch).Quality.CapabilityTier())
			if withPBackup && pb.calls[0].Model != "pb-"+chatTier {
				t.Fatalf("Primary backup quality lost: got %q want %q", pb.calls[0].Model, "pb-"+chatTier)
			}
			if withSBackup && sb.calls[0].Model != "sb-"+dispatchTier {
				t.Fatalf("Secondary backup quality lost: got %q want %q", sb.calls[0].Model, "sb-"+dispatchTier)
			}
		}
	}
}

func TestSecondaryCannotFallbackAndLegacyCoprocUnchanged(t *testing.T) {
	local, primary, secondary := &destinationProvider{name: "local"}, &destinationProvider{name: "primary"}, &destinationProvider{name: "secondary"}
	for _, mode := range []locus.Mode{locus.CloudPrimary, locus.OpenPrimary, locus.CloudOnly, locus.OpenOnly} {
		candidates := Providers{Cloud: primary, Open: local}
		e := NewEngine(provs(candidates), func() locus.Mode { return mode }, nil)
		e.SetModelFor(func(bool, config.Tier) string { return "legacy" })
		if _, err := e.Dispatch(context.Background(), Spec{RoutingTask: config.TaskDispatch}); err == nil {
			t.Fatal("missing Secondary fell back")
		}
	}
	candidates := Providers{Cloud: primary, Open: local, Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: secondary, Profile: "s", IsCloud: true}}}
	e := NewEngine(provs(candidates), func() locus.Mode { return locus.OpenOnly }, nil)
	e.SetDestinationModelFor(func(inference.Selection, config.Tier) string { return "image" })
	if _, err := e.Dispatch(context.Background(), Spec{RoutingTask: config.TaskDispatch}); err == nil {
		t.Fatal("open-only cloud call allowed")
	}
	e = NewEngine(provs(candidates), func() locus.Mode { return locus.CloudPrimary }, nil)
	e.SetModelFor(func(bool, config.Tier) string { return "legacy" })
	if _, err := e.Dispatch(context.Background(), Spec{LocalOffload: true, Role: RoleCoproc}); err != nil {
		t.Fatal(err)
	}
	if len(local.calls) != 1 || len(primary.calls)+len(secondary.calls) != 0 {
		t.Fatal("legacy coproc redirected")
	}
}

func TestInvocationOverridePreservesFallbackQuality(t *testing.T) {
	primary, backup := &destinationProvider{name: "s", fail: true}, &destinationProvider{name: "sb"}
	chain := resilience.New(primary, resilience.Options{Backup: backup, BackupModelFor: func(tier string) string { return "backup-" + tier }})
	e := NewEngine(provs(Providers{Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: chain, Profile: "s", IsCloud: true}}}), func() locus.Mode { return locus.CloudOnly }, nil)
	e.SetDestinationModelFor(func(inference.Selection, config.Tier) string { return "default" })
	_, err := e.Dispatch(context.Background(), Spec{RoutingTask: config.TaskDispatch, Tier: config.TierFastLight, ModelOverride: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	if primary.calls[0].Model != "custom" || backup.calls[0].Model != "backup-fast_light" {
		t.Fatalf("override/fallback: %q -> %q", primary.calls[0].Model, backup.calls[0].Model)
	}
}

func TestPreparedTargetUsesSavedSecondaryDestination(t *testing.T) {
	primary, secondary := &destinationProvider{name: "primary"}, &destinationProvider{name: "secondary"}
	e := NewEngine(provs(Providers{Cloud: primary, Destinations: map[config.Destination]inference.Candidate{config.DestinationPrimary: {Provider: primary, Profile: "p", IsCloud: true}, config.DestinationSecondary: {Provider: secondary, Profile: "s", IsCloud: true}}}), func() locus.Mode { return locus.CloudOnly }, nil)
	e.SetDestinationModelFor(func(sel inference.Selection, tier config.Tier) string { return sel.Profile + ":" + string(tier) })
	target, err := e.PreparedTarget(context.Background(), Spec{RoutingTask: config.TaskDispatch})
	if err != nil || target.Provider != "secondary" || target.Model != "s:most_capable" || len(primary.calls) != 0 || len(secondary.calls) != 0 {
		t.Fatalf("prepared target bypassed destination selection: %+v %v", target, err)
	}
}
