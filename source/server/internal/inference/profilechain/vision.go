package profilechain

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"fmt"
)

// GuardVision binds confirmation to one profile/endpoint before it enters a
// destination chain. Even fixed-capability transports cannot bypass evidence.
func GuardVision(provider inference.Provider, confirmed func(string) bool) inference.Provider {
	return &visionGuard{Provider: provider, confirmed: confirmed}
}

type visionGuard struct {
	inference.Provider
	confirmed func(string) bool
}

func (p *visionGuard) check(req inference.Call) error {
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockImage && (p.confirmed == nil || !p.confirmed(req.Model)) {
				return fmt.Errorf("image capability is unconfirmed for selected profile model %q", req.Model)
			}
		}
	}
	return nil
}
func (p *visionGuard) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	if err := p.check(req); err != nil {
		return inference.Result{}, err
	}
	return p.Provider.Chat(ctx, req)
}
func (p *visionGuard) StreamChat(ctx context.Context, req inference.Call) (inference.Stream, error) {
	if err := p.check(req); err != nil {
		return nil, err
	}
	return p.Provider.StreamChat(ctx, req)
}
