package enterprise

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/modelpolicy"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

func TestAuthorizationUsesCurrentRestrictionsAndLease(t *testing.T) {
	f := newFixture(t, true)
	route := v1.Route{ID: "approved", Provider: "openai", Endpoint: "https://models.example/v1", Model: "approved", Placement: "external"}
	f.routes = []v1.Route{route}
	ctx := context.Background()
	m := f.manager
	attempt := modelpolicy.Attempt{Provider: route.Provider, Endpoint: route.Endpoint, Model: route.Model, Placement: route.Placement}
	if m.Authorize(ctx, attempt) == nil {
		t.Fatal("restart authorized before online verification")
	}
	if err := m.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Authorize(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	pinned, finish, err := m.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	f.routes = []v1.Route{}
	f.revision++
	if err = m.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if len(pinned.Policy.AllowedRoutes) != 1 {
		t.Fatal("turn snapshot changed")
	}
	if m.Authorize(ctx, attempt) == nil {
		t.Fatal("pinned defaults bypassed a new restriction")
	}
	f.routes = []v1.Route{route}
	f.revision++
	if err = m.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.mode = "unavailable"
	if err = m.Sync(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err = m.Authorize(ctx, attempt); err != nil {
		t.Fatal("outage lost valid authorization")
	}
	m.deadline = time.Now().Add(-time.Second)
	if m.Authorize(ctx, attempt) == nil {
		t.Fatal("expired lease authorized")
	}
	f.mode = ""
	if err = m.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.mode = "denied"
	if err = m.Sync(ctx); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if m.Authorize(ctx, attempt) == nil {
		t.Fatal("explicit revocation retained authorization")
	}
	finish()
	f.mode = ""
	if err = m.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Authorize(ctx, attempt) == nil {
		t.Fatal("logout silently became standalone")
	}
}
