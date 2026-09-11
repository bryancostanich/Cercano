package profilechain

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/pkg/config"
	"context"
	"errors"
	"testing"
)

type capture struct {
	calls []llm.ChatRequest
	fail  bool
}

func (*capture) Name() string { return "fixture" }
func (*capture) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsVision: true}
}
func (p *capture) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.calls = append(p.calls, req)
	if p.fail {
		return llm.ChatResponse{}, &llm.Error{Class: llm.ErrQuota, Err: errors.New("quota")}
	}
	return llm.ChatResponse{Model: req.Model}, nil
}
func (*capture) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	panic("not used")
}
func TestImageFailoverUsesIndependentChoice(t *testing.T) {
	for _, image := range []string{"backup-image", ""} {
		t.Run("backup="+image, func(t *testing.T) {
			c := config.Config{ActiveCloudProfile: "p", BackupCloudProfile: "b", CloudProfiles: []config.CloudProfile{{Name: "p", ImageModel: "primary-image"}, {Name: "b", ImageModel: image}}}
			primary, backup := &capture{fail: true}, &capture{}
			provider, err := Build(c, config.DestinationPrimary, func(p config.CloudProfile) (inference.Provider, error) {
				if p.Name == "p" {
					return GuardVision(primary, func(string) bool { return true }), nil
				}
				return GuardVision(backup, func(string) bool { return true }), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Chat(context.Background(), inference.Call{Model: "foreign-text", Tier: string(config.TierVision)})
			if primary.calls[0].Model != "primary-image" {
				t.Fatal("primary ignored image intent")
			}
			if image == "" {
				if err == nil || len(backup.calls) != 0 {
					t.Fatalf("missing backup image leaked request: err=%v calls=%+v", err, backup.calls)
				}
			} else if err != nil || len(backup.calls) != 1 || backup.calls[0].Model != image {
				t.Fatalf("image failover=%+v err=%v", backup.calls, err)
			}
		})
	}
}

func TestUnknownBackupCannotBorrowPrimaryVisionEvidence(t *testing.T) {
	c := config.Config{ActiveCloudProfile: "p", BackupCloudProfile: "b", CloudProfiles: []config.CloudProfile{{Name: "p", BaseURL: "https://one.invalid", ImageModel: "same-id"}, {Name: "b", BaseURL: "https://two.invalid", ImageModel: "same-id"}}}
	primary, backup := &capture{fail: true}, &capture{}
	chain, err := Build(c, config.DestinationPrimary, func(p config.CloudProfile) (inference.Provider, error) {
		raw := primary
		if p.Name == "b" {
			raw = backup
		}
		return GuardVision(raw, func(string) bool { return p.Name == "p" }), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	req := inference.Call{Model: "same-id", Tier: "vision", Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockImage, ImageData: "fixture"}}}}}
	if _, err := chain.Chat(context.Background(), req); err == nil {
		t.Fatal("unknown backup accepted image")
	}
	if len(primary.calls) != 1 || len(backup.calls) != 0 {
		t.Fatalf("calls=%d/%d", len(primary.calls), len(backup.calls))
	}
	if _, err := GuardVision(backup, nil).StreamChat(context.Background(), req); err == nil {
		t.Fatal("stream accepted unconfirmed image")
	}
}

func TestServingRouteUsesActualEndpointEvidence(t *testing.T) {
	c := config.Config{ActiveCloudProfile: "p", BackupCloudProfile: "b", CloudProfiles: []config.CloudProfile{{Name: "p", TierOverrides: map[config.CostTier]string{config.CostPremium: "same-id"}}, {Name: "b", TierOverrides: map[config.CostTier]string{config.CostPremium: "same-id"}}}}
	primary, backup := &capture{fail: true}, &capture{}
	chain, err := Build(c, config.DestinationPrimary, func(p config.CloudProfile) (inference.Provider, error) {
		raw := primary
		window := 16384
		if p.Name == "b" {
			raw = backup
			window = 65536
		}
		return GuardVision(raw, nil, func(string) modelmetadata.Evidence { return modelmetadata.Evidence{ContextWindow: window} }), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	req := inference.Call{Model: "same-id", Tier: "most_capable"}
	before := inference.TargetForCall(chain, req)
	if before.Profile != "p" || before.ContextWindow != 16384 {
		t.Fatalf("initial target: %+v", before)
	}
	response, err := chain.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if response.Route == nil || response.Route.Profile != "b" || response.Route.ContextWindow != 65536 || response.Route.Destination != "primary" {
		t.Fatalf("actual route: %+v", response.Route)
	}
	after := inference.TargetForCall(chain, req)
	if after.Profile != "b" || after.ContextWindow != 65536 {
		t.Fatalf("cooldown target: %+v", after)
	}
}

func TestUnavailablePreferredUsesItsOwnConfiguredBackup(t *testing.T) {
	c := config.Config{SecondaryCloudProfile: "s", SecondaryBackupCloudProfile: "sb", CloudProfiles: []config.CloudProfile{{Name: "s", TierOverrides: map[config.CostTier]string{config.CostPremium: "preferred"}}, {Name: "sb", TierOverrides: map[config.CostTier]string{config.CostPremium: "backup"}}}}
	backup := &capture{}
	chain, err := Build(c, config.DestinationSecondary, func(p config.CloudProfile) (inference.Provider, error) {
		if p.Name == "s" {
			return nil, errors.New("fixture missing credential")
		}
		return backup, nil
	})
	if err != nil {
		t.Fatalf("usable configured backup discarded: %v", err)
	}
	target := inference.TargetForCall(chain, inference.Call{Model: "foreign-override", FallbackTier: "most_capable"})
	if target.Profile != "sb" || target.Model != "backup" {
		t.Fatalf("startup target=%+v", target)
	}
	response, err := chain.Chat(context.Background(), inference.Call{Model: "foreign-override", FallbackTier: "most_capable"})
	if err != nil || len(backup.calls) != 1 || backup.calls[0].Model != "backup" || response.Route.Profile != "sb" {
		t.Fatalf("startup backup: %+v err=%v", response, err)
	}
}

func TestMissingPrimaryQualityDoesNotLeakForeignModel(t *testing.T) {
	c := config.Config{ActiveCloudProfile: "p", BackupCloudProfile: "b", CloudProfiles: []config.CloudProfile{{Name: "p"}, {Name: "b", TierOverrides: map[config.CostTier]string{config.CostPremium: "backup"}}}}
	primary, backup := &capture{}, &capture{}
	chain, err := Build(c, config.DestinationPrimary, func(p config.CloudProfile) (inference.Provider, error) {
		if p.Name == "p" {
			return primary, nil
		}
		return backup, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := inference.Call{Model: "foreign", Tier: "most_capable"}
	target := inference.TargetForCall(chain, request)
	if target.Profile != "b" || target.Model != "backup" {
		t.Fatalf("missing slot target=%+v", target)
	}
	_, err = chain.Chat(context.Background(), request)
	if err != nil || len(primary.calls) != 0 || len(backup.calls) != 1 || backup.calls[0].Model != "backup" {
		t.Fatalf("calls primary=%v backup=%v err=%v", primary.calls, backup.calls, err)
	}
}
