package enterprise

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"cercano/source/server/internal/modelpolicy"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

func fixtureHost(t *testing.T, f *fixture, directory string) *Host {
	t.Helper()
	h, err := NewHost(HostOptions{Directory: directory, Manager: Options{HTTPClient: f.server.Client(), ClientVersion: "1.0.0"}, OpenStore: func() (CredentialStore, error) { return f.store, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}
func approvedFixtureRoute(f *fixture) modelpolicy.Attempt {
	route := v1.Route{ID: "approved", Provider: "openai", Endpoint: "https://models.example/v1", Model: "approved", Placement: "external"}
	f.mu.Lock()
	f.routes = []v1.Route{route}
	f.mu.Unlock()
	return modelpolicy.Attempt{Provider: route.Provider, Endpoint: route.Endpoint, Model: route.Model, Placement: route.Placement}
}
func TestStandaloneHostDoesNotAccessEnterpriseCredentials(t *testing.T) {
	var opens atomic.Int32
	h, err := NewHost(HostOptions{Directory: t.TempDir(), OpenStore: func() (CredentialStore, error) { opens.Add(1); return nil, ErrCredentialStore }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	restore := modelpolicy.Install(h)
	defer restore()
	if h.Status().Managed || modelpolicy.Check(context.Background(), modelpolicy.Attempt{}) != nil {
		t.Fatal("standalone request was changed")
	}
	_, finish, err := h.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Sync(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("activation changed active work: %v", err)
	}
	finish()
	if err = h.Login(context.Background(), "http://remote.example", "invalid", func(string) error { return nil }); err == nil {
		t.Fatal("invalid login accepted")
	}
	if opens.Load() != 0 || h.PolicyManaged() {
		t.Fatal("standalone configuration accessed Keychain or activated")
	}
}
func TestHostPinsBundleAndAppliesNewRestrictions(t *testing.T) {
	f := newFixture(t, true)
	attempt := approvedFixtureRoute(f)
	h := fixtureHost(t, f, t.TempDir())
	if err := h.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, finish, err := h.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	bundle, ok := BundleFromContext(ctx)
	if !ok || bundle.Policy.Revision != 1 || len(bundle.Skills) != 1 {
		t.Fatal("complete bundle not pinned")
	}
	if err = h.Authorize(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err = h.Logout(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("logout changed active turn: %v", err)
	}
	if err = h.UseStandalone(); !errors.Is(err, ErrBusy) {
		t.Fatalf("standalone changed active turn: %v", err)
	}
	f.mu.Lock()
	f.revision++
	f.routes = []v1.Route{}
	f.mu.Unlock()
	if err = h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if h.Authorize(ctx, attempt) == nil {
		t.Fatal("old pinned policy bypassed new restriction")
	}
	again, _ := BundleFromContext(ctx)
	if again.Policy.Revision != 1 || len(again.Policy.AllowedRoutes) != 1 {
		t.Fatal("turn snapshot was rewritten")
	}
	finish()
	if err = h.UseStandalone(); err == nil {
		t.Fatal("standalone discarded connected credentials")
	}
}
func TestHostLogoutAndCredentialLossStayManagedAcrossRestart(t *testing.T) {
	for _, mode := range []string{"logout", "missing_credentials"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, true)
			attempt := approvedFixtureRoute(f)
			dir := t.TempDir()
			h := fixtureHost(t, f, dir)
			if err := h.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if mode == "logout" {
				if err := h.Logout(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			h.Close()
			if mode == "missing_credentials" {
				if err := f.store.Delete(credentialKey); err != nil {
					t.Fatal(err)
				}
			}
			restarted := fixtureHost(t, f, dir)
			restore := modelpolicy.Install(restarted)
			defer restore()
			state := restarted.Status()
			if !state.Managed || !state.EnforcementActive || state.Usable || state.Connected {
				t.Fatalf("incorrect restart status: managed=%v enforcement=%v usable=%v connected=%v", state.Managed, state.EnforcementActive, state.Usable, state.Connected)
			}
			if modelpolicy.Check(context.Background(), attempt) == nil {
				t.Fatal("missing credential restored standalone")
			}
			if err := restarted.UseStandalone(); err != nil {
				t.Fatal(err)
			}
			if restarted.PolicyManaged() || modelpolicy.Check(context.Background(), attempt) != nil {
				t.Fatal("explicit standalone did not restore personal mode")
			}
		})
	}
}
func TestExistingHostSeesAnotherHostActivation(t *testing.T) {
	f := newFixture(t, true)
	attempt := approvedFixtureRoute(f)
	dir := t.TempDir()
	owner := fixtureHost(t, f, dir)
	other := fixtureHost(t, f, dir)
	restore := modelpolicy.Install(other)
	defer restore()
	if modelpolicy.Managed(context.Background()) {
		t.Fatal("initially managed")
	}
	if err := owner.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !modelpolicy.Managed(context.Background()) || modelpolicy.Check(context.Background(), attempt) == nil {
		t.Fatal("already-running host bypassed activation")
	}
	if err := other.Sync(context.Background()); err == nil {
		t.Fatal("two hosts own refresh credentials")
	}
	if err := other.UseStandalone(); err == nil {
		t.Fatal("non-owner cleared active profile")
	}
	if _, err := os.Stat(filepath.Join(dir, "managed")); err != nil {
		t.Fatal("owner marker removed")
	}
}
func TestManagedHostCanRecoverLockedCredentialStore(t *testing.T) {
	f := newFixture(t, true)
	attempt := approvedFixtureRoute(f)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "managed"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var locked atomic.Bool
	locked.Store(true)
	h, err := NewHost(HostOptions{Directory: dir, Manager: Options{HTTPClient: f.server.Client(), ClientVersion: "1.0.0"}, OpenStore: func() (CredentialStore, error) {
		if locked.Load() {
			return nil, ErrCredentialStore
		}
		return f.store, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if !h.Status().Managed || h.Authorize(context.Background(), attempt) == nil {
		t.Fatal("locked store allowed work")
	}
	locked.Store(false)
	if err = h.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = h.Authorize(context.Background(), attempt); err != nil {
		t.Fatal("unlocked connection did not recover:", err)
	}
}

func TestHostRestartRequiresOnlineVerificationEvenWithCachedLease(t *testing.T) {
	f := newFixture(t, true)
	attempt := approvedFixtureRoute(f)
	dir := t.TempDir()
	original := fixtureHost(t, f, dir)
	if err := original.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	original.Close()
	f.mu.Lock()
	f.mode = "unavailable"
	f.mu.Unlock()
	restarted := fixtureHost(t, f, dir)
	if restarted.Authorize(context.Background(), attempt) == nil || restarted.Status().Usable {
		t.Fatal("restart used a cached policy during outage")
	}
	f.mu.Lock()
	f.mode = ""
	f.mu.Unlock()
	if err := restarted.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Authorize(context.Background(), attempt); err != nil {
		t.Fatal("reconnected host did not recover:", err)
	}
}

func TestSyncAndLogoutWithoutAccountLeaveHostStandalone(t *testing.T) {
	f := newFixture(t, false)
	h := fixtureHost(t, f, t.TempDir())
	if err := h.Sync(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("sync without account: %v", err)
	}
	if err := h.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.PolicyManaged() {
		t.Fatal("sync or logout without account activated enterprise")
	}
	if _, err := os.Stat(h.markerPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty account created managed marker")
	}
}
