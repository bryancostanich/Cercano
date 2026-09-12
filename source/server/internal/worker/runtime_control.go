package worker

import (
	"cercano/source/server/pkg/proto"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
)

type runtimeRestartFunc func(context.Context, string) (json.RawMessage, error)
type streamRuntimeControl struct {
	sndr    *sender
	next    atomic.Uint64
	mu      sync.Mutex
	pending map[string]chan *proto.RuntimeRestartToolResponse
}

func newStreamRuntimeControl(sndr *sender) *streamRuntimeControl {
	return &streamRuntimeControl{sndr: sndr, pending: map[string]chan *proto.RuntimeRestartToolResponse{}}
}
func (p *streamRuntimeControl) Restart(ctx context.Context, instanceID string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := strconv.FormatUint(p.next.Add(1), 10)
	ch := make(chan *proto.RuntimeRestartToolResponse, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	p.sndr.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_RuntimeRequest{RuntimeRequest: &proto.RuntimeRestartToolRequest{Id: id, InstanceId: instanceID}}})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-ch:
		if response.GetError() != "" {
			return nil, fmt.Errorf("%s", response.GetError())
		}
		return json.RawMessage(response.GetResultJson()), nil
	}
}
func (p *streamRuntimeControl) deliver(response *proto.RuntimeRestartToolResponse) {
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
