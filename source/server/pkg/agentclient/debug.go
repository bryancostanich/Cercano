package agentclient

import "context"

type debugAdvertisementKey struct{}

// WithDebugAdvertisement advertises development tools for this turn without
// changing availability or permission policy.
func WithDebugAdvertisement(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, debugAdvertisementKey{}, enabled)
}
func debugAdvertisement(ctx context.Context) bool {
	enabled, _ := ctx.Value(debugAdvertisementKey{}).(bool)
	return enabled
}
