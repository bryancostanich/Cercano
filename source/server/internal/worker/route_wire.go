package worker

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

func marshalServingRoute(r *llm.ServingRoute) *proto.ServingRouteInfo {
	if r == nil {
		return nil
	}
	return &proto.ServingRouteInfo{Provider: r.Provider, Profile: r.Profile, Destination: r.Destination, Model: r.Model, ContextWindow: int32(r.ContextWindow), ContextWindowKnown: r.ContextWindowKnown, VisionKnown: r.VisionKnown, SupportsVision: r.SupportsVision}
}
func unmarshalServingRoute(r *proto.ServingRouteInfo) *llm.ServingRoute {
	if r == nil {
		return nil
	}
	return &llm.ServingRoute{Provider: r.Provider, Profile: r.Profile, Destination: r.Destination, Model: r.Model, ContextWindow: int(r.ContextWindow), ContextWindowKnown: r.ContextWindowKnown, VisionKnown: r.VisionKnown, SupportsVision: r.SupportsVision}
}
