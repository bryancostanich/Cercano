package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"cercano/source/server/internal/chatroute"
	"cercano/source/server/internal/inference"
	"cercano/source/server/pkg/proto"
)

type SessionModelSetter interface{ SetSessionModel(chatroute.Control) }

func (w *workerRunner) SetSessionModel(fn chatroute.Control) { w.sessionModel = fn }

type streamSessionModel struct {
	sndr    *sender
	convID  string
	next    atomic.Uint64
	mu      sync.Mutex
	pending map[string]chan *proto.SessionModelResponse
}

func newStreamSessionModel(sndr *sender, convID string) *streamSessionModel {
	return &streamSessionModel{sndr: sndr, convID: convID, pending: make(map[string]chan *proto.SessionModelResponse)}
}
func (p *streamSessionModel) Control(ctx context.Context, convID string, req chatroute.Request) (chatroute.Status, error) {
	if convID != p.convID {
		return chatroute.Status{}, fmt.Errorf("session model control is scoped to the active conversation")
	}
	if err := ctx.Err(); err != nil {
		return chatroute.Status{}, err
	}
	id := strconv.FormatUint(p.next.Add(1), 10)
	ch := make(chan *proto.SessionModelResponse, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	p.sndr.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_SessionModelRequest{SessionModelRequest: &proto.SessionModelRequest{Id: id, ConversationId: p.convID, Action: req.Action, Profile: req.Profile, Model: req.Model}}})
	select {
	case <-ctx.Done():
		return chatroute.Status{}, ctx.Err()
	case response := <-ch:
		if response.GetError() != "" {
			return chatroute.Status{}, fmt.Errorf("%s", response.GetError())
		}
		var result chatroute.Status
		err := json.Unmarshal(response.GetResultJson(), &result)
		return result, err
	}
}
func (p *streamSessionModel) deliver(response *proto.SessionModelResponse) {
	p.mu.Lock()
	ch := p.pending[response.GetId()]
	p.mu.Unlock()
	if ch != nil {
		select {
		case ch <- response:
		default:
		}
	}
}

func (r *workerResolver) ResolveChatRoute(ctx context.Context, route chatroute.Route) (inference.Provider, error) {
	if r.diagnosticBuild == nil {
		return nil, fmt.Errorf("session model provider builder unavailable")
	}
	return chatroute.Build(r.cfgSvc.Get(), route, r.diagnosticBuild)
}
