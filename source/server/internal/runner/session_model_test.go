package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/chatroute"
	"cercano/source/server/internal/inference"
	"cercano/source/server/pkg/config"
)

type sessionResolver struct {
	*fakeResolver
	pinned inference.Provider
	err    error
}

func (r *sessionResolver) ResolveChatRoute(_ context.Context, route chatroute.Route) (inference.Provider, error) {
	if r.err != nil {
		return nil, r.err
	}
	return inference.WithTaskRoute(r.pinned, config.TaskChat, config.Defaults().TaskAssignment(config.TaskChat), config.DestinationPrimary, route.Model), nil
}
func TestSessionModelPinsOnlyMainChatAndClearRestoresDefaults(t *testing.T) {
	normal, pinned := &spyProvider{}, &spyProvider{}
	deps := buildDeps(normal)
	deps.Providers = &sessionResolver{fakeResolver: &fakeResolver{prov: normal}, pinned: pinned}
	core := New(deps)
	req := Request{ConversationID: "pinned", Input: "hello", WorkDir: t.TempDir(), ChatRoute: &chatroute.Route{Profile: "deepinfra", Model: "exact-model"}}
	if _, err := core.RunTurn(t.Context(), req, noopSink{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(normal.requests) != 0 || len(pinned.requests) != 1 || pinned.requests[0].Model != "exact-model" {
		t.Fatalf("wrong route: normal=%d pinned=%+v", len(normal.requests), pinned.requests)
	}
	req.ChatRoute = nil
	if _, err := core.RunTurn(t.Context(), req, noopSink{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(normal.requests) != 1 || len(pinned.requests) != 1 {
		t.Fatal("clear did not restore normal route")
	}
}
func TestSessionModelNeverFallsBack(t *testing.T) {
	for _, test := range []struct {
		name     string
		provider inference.Provider
		err      error
	}{
		{name: "busy", provider: &busyProvider{}},
		{name: "missing-profile", err: errors.New("profile removed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			fallback := &spyProvider{}
			deps := buildDeps(fallback)
			deps.Providers = &sessionResolver{fakeResolver: &fakeResolver{prov: fallback, open: fallback}, pinned: test.provider, err: test.err}
			_, err := New(deps).RunTurn(t.Context(), Request{ConversationID: "pin", Input: "hello", WorkDir: t.TempDir(), ChatRoute: &chatroute.Route{Profile: "deepinfra", Model: "exact-model"}}, noopSink{}, nil, nil)
			if err == nil {
				t.Fatal("pinned route failure hidden")
			}
			if len(fallback.requests) != 0 {
				t.Fatal("pinned route silently fell back")
			}
			if test.err != nil && !strings.Contains(err.Error(), "profile removed") {
				t.Fatal(err)
			}
		})
	}
}
