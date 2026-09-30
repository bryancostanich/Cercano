package worker

// autonomy_proxy.go — the worker-side proxy for the host-owned autonomy ledger.
//
// Autonomous-mode session-control capabilities (suggest_autonomous /
// request_autonomous_execution, capture_decision, auto_exit /
// request_autonomous_exit) read and write an append-only ledger row. The
// architecture keeps exactly ONE owner of that ledger — the host's conversation
// store — because it is durable, user-visible state; the crash-isolated worker
// must never open SQLite. So the worker holds conversation.AutonomyLedger via
// streamAutonomyLedger: every operation is sent over the RunTurn stream as an
// AutonomyLedgerRequest and the host answers with an acknowledged
// AutonomyLedgerResponse carrying the stored/loaded run, correlation and error
// semantics mirroring streamMCPControl / streamRuntimeControl.
//
// A get_active miss comes back found=false and is mapped to sql.ErrNoRows so
// the capability logic (isNoRows) is identical in-process and in-worker.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/proto"
)

// Autonomy ledger operations carried by proto.AutonomyLedgerRequest.op.
const (
	autonomyOpCreate    = "create"
	autonomyOpUpdate    = "update"
	autonomyOpGetActive = "get_active"
)

// streamAutonomyLedger implements conversation.AutonomyLedger by round-tripping
// each operation to the host over the worker stream.
type streamAutonomyLedger struct {
	sndr    *sender
	next    atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan *proto.AutonomyLedgerResponse

	// sendFn overrides the stream send. Production leaves it nil and uses sndr;
	// tests set it to capture the request without standing up a gRPC stream.
	sendFn func(*proto.WorkerToHost)
}

func newStreamAutonomyLedger(sndr *sender) *streamAutonomyLedger {
	return &streamAutonomyLedger{sndr: sndr, pending: map[uint64]chan *proto.AutonomyLedgerResponse{}}
}

// emit puts one message on the wire via the test hook when set, else the sender.
func (p *streamAutonomyLedger) emit(m *proto.WorkerToHost) {
	if p.sendFn != nil {
		p.sendFn(m)
		return
	}
	p.sndr.send(m)
}

// call sends one ledger operation and blocks for the acknowledged host response
// or context cancellation. The host applies the operation to its store where
// the SQLite database lives, so this sees only a completed outcome.
func (p *streamAutonomyLedger) call(ctx context.Context, op string, run *conversation.AutonomyRun, convID string) (*proto.AutonomyLedgerResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var runJSON []byte
	if run != nil {
		b, err := json.Marshal(run)
		if err != nil {
			return nil, fmt.Errorf("autonomy ledger %s: encode run: %w", op, err)
		}
		runJSON = b
	}
	id := p.next.Add(1)
	ch := make(chan *proto.AutonomyLedgerResponse, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()

	p.emit(&proto.WorkerToHost{Msg: &proto.WorkerToHost_AutonomyRequest{AutonomyRequest: &proto.AutonomyLedgerRequest{
		Id:             id,
		Op:             op,
		RunJson:        runJSON,
		ConversationId: convID,
	}}})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if e := resp.GetError(); e != "" {
			return nil, errors.New(e)
		}
		return resp, nil
	}
}

func (p *streamAutonomyLedger) deliver(resp *proto.AutonomyLedgerResponse) {
	p.mu.Lock()
	ch := p.pending[resp.GetId()]
	p.mu.Unlock()
	if ch != nil {
		select {
		case ch <- resp:
		default:
		}
	}
}

// CreateAutonomyRun satisfies conversation.AutonomyLedger: the host inserts the
// append-only row (assigning its run id) and returns the stored run.
func (p *streamAutonomyLedger) CreateAutonomyRun(ctx context.Context, r conversation.AutonomyRun) (conversation.AutonomyRun, error) {
	resp, err := p.call(ctx, autonomyOpCreate, &r, r.ConversationID)
	if err != nil {
		return conversation.AutonomyRun{}, fmt.Errorf("create autonomy run: %w", err)
	}
	return decodeLedgerRun(resp, r)
}

// UpdateAutonomyRun satisfies conversation.AutonomyLedger: the host updates the
// row by run id; the acknowledged response echoes the updated run.
func (p *streamAutonomyLedger) UpdateAutonomyRun(ctx context.Context, r conversation.AutonomyRun) error {
	resp, err := p.call(ctx, autonomyOpUpdate, &r, r.ConversationID)
	if err != nil {
		return fmt.Errorf("update autonomy run: %w", err)
	}
	_, err = decodeLedgerRun(resp, r)
	return err
}

// GetActiveAutonomyRun satisfies conversation.AutonomyLedger. found=false maps
// to sql.ErrNoRows, matching the in-process store's miss contract.
func (p *streamAutonomyLedger) GetActiveAutonomyRun(ctx context.Context, conversationID string) (conversation.AutonomyRun, error) {
	resp, err := p.call(ctx, autonomyOpGetActive, nil, conversationID)
	if err != nil {
		return conversation.AutonomyRun{}, fmt.Errorf("get active autonomy run: %w", err)
	}
	if !resp.GetFound() {
		return conversation.AutonomyRun{}, sql.ErrNoRows
	}
	return decodeLedgerRun(resp, conversation.AutonomyRun{})
}

// decodeLedgerRun unmarshals the response's stored run, falling back to the
// request-side run when the host echoed no payload.
func decodeLedgerRun(resp *proto.AutonomyLedgerResponse, fallback conversation.AutonomyRun) (conversation.AutonomyRun, error) {
	raw := resp.GetRunJson()
	if len(raw) == 0 {
		return fallback, nil
	}
	var out conversation.AutonomyRun
	if err := json.Unmarshal(raw, &out); err != nil {
		return conversation.AutonomyRun{}, fmt.Errorf("decode autonomy ledger run: %w", err)
	}
	return out, nil
}
