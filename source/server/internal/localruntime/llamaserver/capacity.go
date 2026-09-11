package llamaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"cercano/source/server/internal/localruntime"
)

// readServingCapacity requires agreement between the documented per-request
// props field and every slot. n_ctx is already per-request; never divide it by
// the slot count (unified KV slots can each advertise the entire allocation).
func (p *Provider) readServingCapacity(ctx context.Context, endpoint string) (int, error) {
	var props struct {
		Default struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
		Slots int `json:"total_slots"`
	}
	var slots []struct {
		NCtx int `json:"n_ctx"`
	}
	get := func(path string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
		if err != nil {
			return err
		}
		res, err := p.client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("%s returned HTTP %d", path, res.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
	}
	if err := get("/props", &props); err != nil {
		return 0, fmt.Errorf("cannot confirm llama-server capacity: %w", err)
	}
	if err := get("/slots", &slots); err != nil {
		return 0, fmt.Errorf("cannot confirm llama-server slots: %w", err)
	}
	if props.Default.NCtx <= 0 || props.Slots <= 0 || props.Slots != len(slots) {
		return 0, fmt.Errorf("missing or inconsistent llama-server capacity properties")
	}
	for _, slot := range slots {
		if slot.NCtx != props.Default.NCtx {
			return 0, fmt.Errorf("llama-server properties and per-slot capacity disagree")
		}
	}
	return props.Default.NCtx, nil
}

func (p *Provider) confirmCapacity(ctx context.Context, id, endpoint string) error {
	p.mu.Lock()
	inst := p.running[id]
	if inst == nil {
		p.mu.Unlock()
		return fmt.Errorf("instance disappeared before confirmation")
	}
	before := inst.record
	invalidateCapacity(&inst.record)
	p.mu.Unlock()
	n, err := p.readServingCapacity(ctx, endpoint)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.running[id]
	if current != inst || current.stopping || current.record.PID != before.PID || !current.record.StartedAt.Equal(before.StartedAt) || current.record.Endpoint != endpoint {
		return fmt.Errorf("instance changed during capacity confirmation")
	}
	switch current.record.State {
	case localruntime.InstanceStarting, localruntime.InstanceRunning, localruntime.InstanceHealthy:
	default:
		return fmt.Errorf("instance is no longer serving")
	}
	if current.record.Context.PlannedTokens > 0 && n > current.record.Context.PlannedTokens {
		return fmt.Errorf("serving context %d exceeds checked allocation %d", n, current.record.Context.PlannedTokens)
	}
	current.record.Context.ConfirmedTokens = n
	current.record.Context.ConfirmedAt = time.Now()
	return nil
}

func invalidateCapacity(record *localruntime.InstanceRecord) {
	record.Context.ConfirmedTokens = 0
	record.Context.ConfirmedAt = time.Time{}
}
