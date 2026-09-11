package worker

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/pkg/proto"
	"context"
	pb "google.golang.org/protobuf/proto"
	"testing"
)

type routeEvents struct{ events []llm.StreamEvent }

func (s *routeEvents) Next() (llm.StreamEvent, bool, error) {
	if len(s.events) == 0 {
		return llm.StreamEvent{}, false, nil
	}
	e := s.events[0]
	s.events = s.events[1:]
	return e, true, nil
}
func (*routeEvents) Close() error { return nil }
func TestServingRouteSurvivesStreamAndWorkerBoundaries(t *testing.T) {
	route := &llm.ServingRoute{Provider: "fixture", Profile: "secondary-backup", Destination: "secondary", Model: "shared-id", ContextWindow: 32768, ContextWindowKnown: true, VisionKnown: true, SupportsVision: true}
	event := llm.StreamEvent{Type: llm.EventMessageStart, Route: route}
	serialized, err := pb.Marshal(MarshalStreamEvent(event))
	if err != nil {
		t.Fatal(err)
	}
	var wire proto.LLMStreamEvent
	if err := pb.Unmarshal(serialized, &wire); err != nil {
		t.Fatal(err)
	}
	stream := &routeEvents{events: []llm.StreamEvent{UnmarshalStreamEvent(&wire), {Type: llm.EventMessageStop, StopReason: "end_turn"}}}
	response, err := llm.CollectStream(context.Background(), stream, nil, nil)
	if err != nil || response.Route == nil || *response.Route != *route || response.Model != "shared-id" {
		t.Fatalf("stream route=%+v err=%v", response.Route, err)
	}
	done := runner.Event{Kind: runner.EventDone, Result: runner.Result{Route: route, Model: "fixture"}}
	result := UnmarshalEvent(MarshalEvent(done)).Result
	if result.Route == nil || *result.Route != *route {
		t.Fatal("worker event lost actual route")
	}
	request := llm.ChatRequest{Model: "custom", FallbackTier: "fast_light"}
	wireReq, err := MarshalChatRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalChatRequest(wireReq)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.FallbackTier != "fast_light" || decoded.Tier != "" {
		t.Fatal("worker proxy lost override fallback intent")
	}
}
