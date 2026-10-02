package builtins

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/chatroute"
)

func TestSessionModelPermissionAndConversationScope(t *testing.T) {
	cap := sessionModelCap{}
	for _, tc := range []struct {
		args string
		tier capabilities.Tier
	}{
		{`{}`, capabilities.TierR}, {`{"action":"status"}`, capabilities.TierR},
		{`{"action":"models","profile":"deepinfra"}`, capabilities.TierR},
		{`{"action":"set","profile":"deepinfra","model":"exact-model"}`, capabilities.TierX},
		{`{"action":"clear"}`, capabilities.TierX}, {`{"profile":"deepinfra"}`, capabilities.TierX},
		{`broken`, capabilities.TierX},
	} {
		if got := cap.TierFor(json.RawMessage(tc.args)); got != tc.tier {
			t.Fatalf("%s tier=%s", tc.args, got)
		}
	}
	calls := 0
	svc := capabilities.Services{SessionModel: func(_ context.Context, convID string, req chatroute.Request) (chatroute.Status, error) {
		calls++
		if convID != "current" || req.Profile != "deepinfra" || req.Model != "exact-model" {
			t.Fatalf("scope or target lost: %s %+v", convID, req)
		}
		return chatroute.Status{Override: &chatroute.Route{Profile: req.Profile, Model: req.Model}}, nil
	}}
	args := json.RawMessage(`{"action":"set","profile":"deepinfra","model":"exact-model","conversation_id":"other"}`)
	if _, err := cap.Execute(t.Context(), &capabilities.Call{ConversationID: "current", Args: args, Svc: svc}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("control not invoked")
	}
	if _, err := cap.Execute(t.Context(), &capabilities.Call{Args: args, Svc: svc}); err == nil {
		t.Fatal("missing conversation accepted")
	}
}
