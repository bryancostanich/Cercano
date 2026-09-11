package profilechain

import (
	"cercano/source/server/internal/inference"
	"context"
)

// A non-nil sentinel for a preferred provider that could not be constructed.
// The resilience engine starts on its separately configured backup in this case.
type unavailableProvider struct{ err error }

func (*unavailableProvider) Name() string                         { return "unavailable preferred provider" }
func (*unavailableProvider) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (p *unavailableProvider) Chat(context.Context, inference.Call) (inference.Result, error) {
	return inference.Result{}, p.err
}
func (p *unavailableProvider) StreamChat(context.Context, inference.Call) (inference.Stream, error) {
	return nil, p.err
}
