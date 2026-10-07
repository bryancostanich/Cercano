package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/proto"
)

// hydrationStubAgent delays hydration unconditionally — it does NOT watch the
// RPC context, modelling hydration work that keeps running after the client
// disconnects and the RPC returns. hydrateErr is returned once released;
// started records that hydration began.
type hydrationStubAgent struct {
	store       conversation.Store
	hydrateWait chan struct{}
	hydrateErr  error
	started     atomic.Bool
}

func (f *hydrationStubAgent) PersistentStore() conversation.Store { return f.store }
func (f *hydrationStubAgent) ListConversations(context.Context, string, int) ([]conversation.Info, error) {
	return nil, nil
}
func (f *hydrationStubAgent) GetConversation(context.Context, string) (conversation.Info, error) {
	return conversation.Info{}, nil
}
func (f *hydrationStubAgent) ResumeConversation(ctx context.Context, conversationID string) ([]conversation.Turn, error) {
	f.started.Store(true)
	<-f.hydrateWait
	return nil, f.hydrateErr
}
func (f *hydrationStubAgent) DeleteConversation(context.Context, string) error { return nil }
func (f *hydrationStubAgent) RenameConversation(context.Context, string, string) error {
	return nil
}
func (f *hydrationStubAgent) IsCompacting(string) bool  { return false }
func (f *hydrationStubAgent) ScheduleCompaction(string) {}

// countingAdmission tracks acquire/release calls. Its release intentionally
// guards nothing itself, so a double release from the service would be
// observable as released > acquired.
type countingAdmission struct {
	mu       sync.Mutex
	acquired int
	released int
	refuse   error
}

func (c *countingAdmission) acquire() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acquired++
	if c.refuse != nil {
		return nil, c.refuse
	}
	return func() {
		c.mu.Lock()
		c.released++
		c.mu.Unlock()
	}, nil
}

func (c *countingAdmission) snapshot() (acquired, released int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acquired, c.released
}

func (c *countingAdmission) releasedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.released
}

func newHydrationTestStore(t *testing.T, convID string, turns int) conversation.Store {
	t.Helper()
	ctx := context.Background()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureConversation(ctx, convID, "/tmp/project", "model"); err != nil {
		t.Fatalf("ensure conversation: %v", err)
	}
	for i := 0; i < turns; i++ {
		if err := store.Append(ctx, conversation.Turn{
			ID:             fmt.Sprintf("turn-%d", i),
			ConversationID: convID,
			Role:           "assistant",
			Content:        fmt.Sprintf("turn-%d", i),
			CreatedAt:      time.Unix(int64(i), 0),
		}); err != nil {
			t.Fatalf("append turn: %v", err)
		}
	}
	return store
}

// TestResumeHydrationLeaseOutlivesRPCDisconnect verifies the worker's admitted
// lifetime: after the stream context is cancelled and the RPC returns, the
// lease is still held until hydration actually returns.
func TestResumeHydrationLeaseOutlivesRPCDisconnect(t *testing.T) {
	const convID = "conv-disconnect"
	store := newHydrationTestStore(t, convID, 2)

	hydrateWait := make(chan struct{})
	agent := &hydrationStubAgent{store: store, hydrateWait: hydrateWait}
	admission := &countingAdmission{}
	svc := &svc{convAgent: agent}
	if err := svc.BindResumeHydrationWork(admission.acquire); err != nil {
		t.Fatalf("BindResumeHydrationWork() = %v, want nil", err)
	}

	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &viewportResumeFakeStream{ctx: streamCtx}
	done := make(chan error, 1)
	go func() {
		done <- svc.StreamResumeConversationViewportFirst(
			&proto.ResumeConversationViewportFirstRequest{ConversationId: convID, TailTurns: 2}, stream)
	}()

	// Wait until the worker has been spawned and its lease acquired.
	deadline := time.After(2 * time.Second)
	for {
		acquired, released := admission.snapshot()
		if acquired == 1 && agent.started.Load() {
			_ = released
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for hydration worker to start; acquired=%d released=%d", acquired, released)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Disconnect: the RPC returns on ctx cancellation while hydration is still
	// blocked. The worker lease must survive the RPC.
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("StreamResumeConversationViewportFirst() = nil, want ctx error after disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not return after disconnect")
	}
	if got := admission.releasedCount(); got != 0 {
		t.Fatalf("lease released %d times after RPC return but before hydration returned, want 0", got)
	}

	// Let hydration actually return; the worker must release exactly then.
	close(hydrateWait)
	deadline = time.After(2 * time.Second)
	for admission.releasedCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for worker lease release after hydration returned")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if got := admission.releasedCount(); got != 1 {
		t.Fatalf("lease released %d times, want 1", got)
	}
}

// TestResumeHydrationRefusedAdmissionStartsNothing verifies that when the
// admission hook refuses, hydration never starts and nothing leaks.
func TestResumeHydrationRefusedAdmissionStartsNothing(t *testing.T) {
	const convID = "conv-refused"
	store := newHydrationTestStore(t, convID, 2)

	hydrateWait := make(chan struct{})
	defer close(hydrateWait) // if hydration ever starts, unblock the test
	agent := &hydrationStubAgent{store: store, hydrateWait: hydrateWait}
	admission := &countingAdmission{refuse: errors.New("updates preparing: work refused")}
	svc := &svc{convAgent: agent}
	if err := svc.BindResumeHydrationWork(admission.acquire); err != nil {
		t.Fatalf("BindResumeHydrationWork() = %v, want nil", err)
	}

	stream := &viewportResumeFakeStream{ctx: context.Background()}
	err := svc.StreamResumeConversationViewportFirst(
		&proto.ResumeConversationViewportFirstRequest{ConversationId: convID, TailTurns: 2}, stream)
	if err == nil {
		t.Fatal("StreamResumeConversationViewportFirst() = nil, want admission refusal error")
	}
	if agent.started.Load() {
		t.Fatal("hydration started despite refused admission")
	}
	if acquired, released := admission.snapshot(); acquired != 1 || released != 0 {
		t.Fatalf("admission acquired=%d released=%d, want 1/0 (refused acquire releases nothing)", acquired, released)
	}
}

// TestResumeHydrationErrorReleasesOnce verifies that a hydration error
// releases the worker lease exactly once.
func TestResumeHydrationErrorReleasesOnce(t *testing.T) {
	const convID = "conv-error"
	store := newHydrationTestStore(t, convID, 2)

	hydrateErr := errors.New("hydrate failed")
	agent := &hydrationStubAgent{
		store:       store,
		hydrateWait: make(chan struct{}),
		hydrateErr:  hydrateErr,
	}
	close(agent.hydrateWait)
	admission := &countingAdmission{}
	svc := &svc{convAgent: agent}
	if err := svc.BindResumeHydrationWork(admission.acquire); err != nil {
		t.Fatalf("BindResumeHydrationWork() = %v, want nil", err)
	}

	stream := &viewportResumeFakeStream{ctx: context.Background()}
	err := svc.StreamResumeConversationViewportFirst(
		&proto.ResumeConversationViewportFirstRequest{ConversationId: convID, TailTurns: 2}, stream)
	if err == nil {
		t.Fatal("StreamResumeConversationViewportFirst() = nil, want hydration error")
	}
	if acquired, released := admission.snapshot(); acquired != 1 || released != 1 {
		t.Fatalf("admission acquired=%d released=%d, want 1/1", acquired, released)
	}
}

// TestBindResumeHydrationWorkValidation covers the startup-only contract: nil
// hooks and rebinding are refused, and binding is refused while an untracked
// hydration worker is still running, but succeeds once idle again.
func TestBindResumeHydrationWorkValidation(t *testing.T) {
	const convID = "conv-bind"
	store := newHydrationTestStore(t, convID, 2)

	if err := (&svc{}).BindResumeHydrationWork(nil); err == nil {
		t.Fatal("BindResumeHydrationWork(nil) = nil, want error")
	}

	hydrateWait := make(chan struct{})
	agent := &hydrationStubAgent{store: store, hydrateWait: hydrateWait}
	tracked := &svc{convAgent: agent}
	if err := tracked.BindResumeHydrationWork(func() (func(), error) { return func() {}, nil }); err != nil {
		t.Fatalf("BindResumeHydrationWork() = %v, want nil", err)
	}
	if err := tracked.BindResumeHydrationWork(func() (func(), error) { return func() {}, nil }); err == nil {
		t.Fatal("rebinding admitted hydration work = nil, want error")
	}

	// Unbound services still count active hydration, so a late attach while
	// work runs must be refused rather than pretending the service is idle.
	svc2 := &svc{convAgent: &hydrationStubAgent{store: store, hydrateWait: hydrateWait}}
	stream := &viewportResumeFakeStream{ctx: context.Background()}
	done := make(chan error, 1)
	go func() {
		done <- svc2.StreamResumeConversationViewportFirst(
			&proto.ResumeConversationViewportFirstRequest{ConversationId: convID, TailTurns: 2}, stream)
	}()
	deadline := time.After(2 * time.Second)
	for !svc2.convAgent.(*hydrationStubAgent).started.Load() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for hydration worker to start")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := svc2.BindResumeHydrationWork(func() (func(), error) { return func() {}, nil }); err == nil {
		t.Fatal("late bind while hydration active = nil, want error")
	}
	close(hydrateWait)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not finish after hydration release")
	}
	if err := svc2.BindResumeHydrationWork(func() (func(), error) { return func() {}, nil }); err != nil {
		t.Fatalf("bind after hydration idle = %v, want nil", err)
	}
}
