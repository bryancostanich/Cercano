package profilechain

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/modelmetadata"
	"context"
	"fmt"
)

// GuardVision binds confirmation to one profile/endpoint before it enters a
// destination chain. Even fixed-capability transports cannot bypass evidence.
func GuardVision(provider inference.Provider, confirmed func(string) bool, evidence ...func(string) modelmetadata.Evidence) inference.Provider {
	p := &visionGuard{Provider: provider, confirmed: confirmed}
	if len(evidence) > 0 {
		p.evidence = evidence[0]
	}
	return p
}

type visionGuard struct {
	inference.Provider
	confirmed func(string) bool
	evidence  func(string) modelmetadata.Evidence
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

func (p *visionGuard) ModelEvidence(model string) modelmetadata.Evidence {
	if p.evidence == nil {
		if p.confirmed != nil && p.confirmed(model) {
			return modelmetadata.Evidence{Vision: modelmetadata.VisionSupported}
		}
		return modelmetadata.Evidence{}
	}
	return p.evidence(model)
}
