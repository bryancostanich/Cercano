package permissions_test

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/hostsvc/permissions"
)

func TestBroker_NilStore(t *testing.T) {
	b := permissions.New(nil, nil, nil)
	if got := b.Mode(); got != agent.ModePermissive {
		t.Errorf("nil store: Mode() = %q, want %q", got, agent.ModePermissive)
	}
	if err := b.SetMode(agent.ModeStrict); err == nil {
		t.Error("nil store: SetMode should return error")
	}
	if b.HasPending() {
		t.Error("nil pending: HasPending() should be false")
	}
	if b.Store() != nil {
		t.Error("nil store: Store() should return nil")
	}
}

func TestBroker_ResolveWait(t *testing.T) {
	pending := agent.NewPendingDecisions()
	b := permissions.New(nil, pending, nil)

	if !b.HasPending() {
		t.Fatal("HasPending() should be true when pending is set")
	}

	// Resolve fires in a goroutine after a brief sleep so the main goroutine
	// reaches Wait first (matching the pattern in pending_test.go).
	go func() {
		time.Sleep(10 * time.Millisecond)
		ok := b.Resolve("tool-1", agent.Decision{Allow: true, Persist: false})
		if !ok {
			t.Errorf("Resolve returned false; no waiter registered")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	d, err := b.Wait(ctx, "tool-1")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !d.Allow {
		t.Errorf("expected Allow=true, got %+v", d)
	}
}

func TestBroker_BroadcastDedup(t *testing.T) {
	calls := 0
	broadcast := func(mode string) { calls++ }
	store, err := agent.LoadPermissionStore(t.TempDir() + "/perms.yaml")
	if err != nil {
		t.Fatalf("LoadPermissionStore: %v", err)
	}

	b := permissions.New(store, nil, broadcast)

	if err := b.SetMode(agent.ModeStrict); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 broadcast after SetMode; got %d", calls)
	}
	// Same mode again — dedupe should suppress.
	if err := b.SetMode(agent.ModeStrict); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected still 1 broadcast after duplicate SetMode; got %d", calls)
	}
}
