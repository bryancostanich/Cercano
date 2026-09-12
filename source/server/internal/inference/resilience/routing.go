package resilience

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"fmt"
)

func (p *Provider) backupUnavailable(req inference.Call) error {
	if req.FallbackTier != "" {
		req.Tier = req.FallbackTier
	}
	if p.backupModelFor != nil && p.backupModelFor(req.Tier) == "" {
		return fmt.Errorf("backup model unavailable for requested tier %q", req.Tier)
	}
	if req.Tier == "vision" {
		target := inference.TargetForCall(p.backup, p.backupRequest(req))
		if target.Profile != "" && (!target.VisionKnown || !target.SupportsVision) {
			return fmt.Errorf("backup image capability is unconfirmed")
		}
	}
	return nil
}

func (p *Provider) backupChat(ctx context.Context, req inference.Call) (inference.Result, error) {
	if err := p.backupUnavailable(req); err != nil {
		return inference.Result{}, err
	}
	return p.backup.Chat(ctx, p.backupRequest(req))
}

func (p *Provider) backupStream(ctx context.Context, req inference.Call) (inference.Stream, error) {
	if err := p.backupUnavailable(req); err != nil {
		return nil, err
	}
	return p.backup.StreamChat(ctx, p.backupRequest(req))
}

func (p *Provider) TargetFor(model, tier string) llm.ServingRoute {
	return p.TargetForCall(inference.Call{Model: model, Tier: tier})
}

func (p *Provider) useBackup(req inference.Call) bool {
	if p.backup == nil || p.primaryBlocked {
		return false
	}
	if p.primaryUnavailable || p.quotaCoolingDown() || (req.Tier != "" && p.primaryModelFor != nil && p.primaryModelFor(req.Tier) == "") {
		return true
	}
	if req.Tier == "vision" {
		target := inference.TargetForCall(p.primary, p.primaryRequest(req))
		if target.Profile != "" && (!target.VisionKnown || !target.SupportsVision) {
			return true
		}
	}
	return false
}

func (p *Provider) TargetForCall(req inference.Call) llm.ServingRoute {
	if p.useBackup(req) {
		if p.backupUnavailable(req) != nil {
			req.Model = ""
			return inference.TargetForCall(p.backup, req)
		}
		return inference.TargetForCall(p.backup, p.backupRequest(req))
	}
	return inference.TargetForCall(p.primary, p.primaryRequest(req))
}

func (p *Provider) TargetForContext(ctx context.Context, req inference.Call) llm.ServingRoute {
	if llm.AuthFallbackSelected(ctx, p) {
		if !p.authenticationBackupAllowed(req) {
			return llm.ServingRoute{}
		}
		return inference.TargetForContext(ctx, p.backup, p.backupRequest(req))
	}
	return p.TargetForCall(req)
}
