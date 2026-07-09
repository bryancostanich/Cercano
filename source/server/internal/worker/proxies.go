package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	proto "cercano/source/server/pkg/proto"
)

// sendBufSize is the bounded buffer for the serialised sender goroutine.
// All three proxy types (event, permission, persist) funnel through it so the
// single gRPC stream.Send is called from exactly one goroutine.
const sendBufSize = 128

// sender owns the single goroutine that calls stream.Send.
// gRPC BidiStreamingServer.Send is NOT safe for concurrent calls.
type sender struct {
	ch     chan *proto.WorkerToHost
	stream proto.Worker_RunTurnServer
	done   chan struct{}
	once   sync.Once
}

func newSender(stream proto.Worker_RunTurnServer) *sender {
	s := &sender{
		ch:     make(chan *proto.WorkerToHost, sendBufSize),
		stream: stream,
		done:   make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *sender) run() {
	defer close(s.done)
	for msg := range s.ch {
		if err := s.stream.Send(msg); err != nil {
			// Stream closed — drain remaining messages without sending.
			for range s.ch {
			}
			return
		}
	}
}

// send enqueues a message. Blocks if the buffer is full (host expected to drain promptly).
func (s *sender) send(msg *proto.WorkerToHost) {
	s.ch <- msg
}

// close shuts down the sender and waits for the goroutine to finish flushing.
// Safe to call multiple times (sync.Once).
func (s *sender) close() {
	s.once.Do(func() { close(s.ch) })
	<-s.done
}

// ─── streamEventSink ──────────────────────────────────────────────────────────

// streamEventSink implements runner.EventSink by sending WorkerEvents over the stream.
type streamEventSink struct{ sndr *sender }

func (e *streamEventSink) Emit(ev runner.Event) {
	we := MarshalEvent(ev)
	e.sndr.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_Event{Event: we}})
}

// ─── streamPermissionRequester ────────────────────────────────────────────────

// streamPermissionRequester round-trips PermissionRequest↔PermissionResponse over the stream.
// It assigns a monotonic ID, sends the request, and blocks until the host responds.
type streamPermissionRequester struct {
	sndr    *sender
	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan permResult
}

type permResult struct {
	allow bool
	err   error
}

func newStreamPermissionRequester(sndr *sender) *streamPermissionRequester {
	return &streamPermissionRequester{
		sndr:    sndr,
		pending: make(map[uint64]chan permResult),
	}
}

// Request satisfies the runner.PermissionRequester function type.
func (p *streamPermissionRequester) Request(
	ctx context.Context,
	toolUseID, name string,
	args json.RawMessage,
	tier llm.Permission,
	destructive bool,
) (bool, error) {
	id := p.nextID.Add(1)
	ch := make(chan permResult, 1)

	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()

	req := &proto.PermissionRequest{
		Id:          id,
		ToolUseId:   toolUseID,
		Name:        name,
		ArgsJson:    string(args),
		Tier:        string(tier),
		Destructive: destructive,
	}
	p.sndr.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_PermRequest{PermRequest: req}})

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case r := <-ch:
		return r.allow, r.err
	}
}

// deliver routes a PermissionResponse to the pending requester with matching ID.
func (p *streamPermissionRequester) deliver(resp *proto.PermissionResponse) {
	p.mu.Lock()
	ch, ok := p.pending[resp.GetId()]
	p.mu.Unlock()
	if !ok {
		return // stale response; ignore
	}
	var err error
	if e := resp.GetError(); e != "" {
		err = fmt.Errorf("%s", e)
	}
	ch <- permResult{allow: resp.GetAllow(), err: err}
}

// ─── streamCredentialSource ───────────────────────────────────────────────────

// streamCredentialSource round-trips CredentialRequest↔CredentialResponse over
// the stream. It is the mirror of streamPermissionRequester: monotonic id,
// per-id response channel, serialised send via sndr, ctx-cancel aware.
// The worker holds NO durable credential; it fetches on demand so the host owns
// refresh and ChatGPT-subscription OAuth with no special-casing in the worker.
type streamCredentialSource struct {
	sndr    *sender
	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan credResult
}

type credResult struct {
	token   string
	account string
	err     error
}

func newStreamCredentialSource(sndr *sender) *streamCredentialSource {
	return &streamCredentialSource{
		sndr:    sndr,
		pending: make(map[uint64]chan credResult),
	}
}

// Fetch asks the host to resolve/refresh the credential for profileName.
// Returns (token, accountID, err). For static-key profiles accountID is "".
func (c *streamCredentialSource) Fetch(ctx context.Context, profileName string) (token, accountID string, err error) {
	id := c.nextID.Add(1)
	ch := make(chan credResult, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	c.sndr.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_CredRequest{CredRequest: &proto.CredentialRequest{
		Id:          id,
		ProfileName: profileName,
	}}})

	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	case r := <-ch:
		return r.token, r.account, r.err
	}
}

// deliver routes a CredentialResponse to the pending Fetch with matching ID.
func (c *streamCredentialSource) deliver(resp *proto.CredentialResponse) {
	c.mu.Lock()
	ch, ok := c.pending[resp.GetId()]
	c.mu.Unlock()
	if !ok {
		return // stale or unmatched; ignore
	}
	var err error
	if e := resp.GetError(); e != "" {
		err = fmt.Errorf("%s", e)
	}
	ch <- credResult{token: resp.GetToken(), account: resp.GetAccount(), err: err}
}

// ─── streamTokenSource ────────────────────────────────────────────────────────

// streamTokenSource implements responses.TokenSource by proxying to
// streamCredentialSource. Used for ChatGPT-subscription profiles where the
// host holds the OAuth token set and handles refresh.
type streamTokenSource struct {
	creds       *streamCredentialSource
	profileName string
}

// Token satisfies responses.TokenSource: returns (access, accountID, err).
func (s *streamTokenSource) Token(ctx context.Context) (access, accountID string, err error) {
	return s.creds.Fetch(ctx, s.profileName)
}

// ─── streamPersistFunc ────────────────────────────────────────────────────────

// streamPersistFunc sends PersistTurn messages over the stream.
type streamPersistFunc struct {
	sndr *sender
	gen  uint64
}

func (pf *streamPersistFunc) persist(m llm.Message) {
	msg, err := MarshalMessage(m)
	if err != nil {
		return // best-effort
	}
	pf.sndr.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_Persist{Persist: &proto.PersistTurn{
		Message: msg,
		Gen:     pf.gen,
	}}})
}

// ─── preloadedHistory ─────────────────────────────────────────────────────────

// preloadedHistory implements runner.TurnHistory for the worker.
// History and project context were pre-assembled by the host and sent in StartTurn.
type preloadedHistory struct {
	history        []llm.Message
	projectContext string
	pf             *streamPersistFunc
}

func (h *preloadedHistory) AssembleHistory(_ context.Context, _ string) []llm.Message {
	return h.history
}

func (h *preloadedHistory) LoadProjectContext(_ string) string {
	return h.projectContext
}

func (h *preloadedHistory) PersistTurn(_ context.Context, _ string, m llm.Message) {
	h.pf.persist(m)
}
