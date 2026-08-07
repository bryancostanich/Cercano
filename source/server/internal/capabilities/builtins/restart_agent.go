package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
)

// restartAgentCap bounces the singleton agent process: it drains in-flight
// turns, stops runtime children (llama-server instances), and exits, letting
// the CLI's reconnect loop auto-launch a fresh agent. It is the model-invokable
// surface for a clean self-bounce — e.g. after the agent binary is rebuilt, or
// to clear accumulated runtime state.
//
// The bounce is asynchronous: the RestartAgent hook only SCHEDULES the shutdown
// (a self-SIGTERM after a short delay) and returns immediately, so this Result
// flushes back to the model and the confirming CLI before the listener closes.
// Every attached CLI then sees the disconnect and reconnects to the fresh
// process. Nothing here waits for the exit — the process is gone by the time
// the delay elapses.
type restartAgentCap struct{}

// RestartAgent constructs the restart_agent capability. X-tier (disruptive: the
// bounce severs every in-flight turn and every attached CLI's connection), so
// it always confirms at the y/n/d/c gate even under tiered bypass. Agent
// surface only — restarting the host process from an external MCP client is not
// meaningful.
func RestartAgent() capabilities.Capability { return restartAgentCap{} }

func (restartAgentCap) Name() string                   { return "restart_agent" }
func (restartAgentCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (restartAgentCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (restartAgentCap) Description() string {
	return "Restart the singleton agent process: drains in-flight turns, stops runtime (llama-server) children, and exits so the CLI reconnect loop launches a fresh agent. Disruptive — severs every in-flight turn and every attached CLI connection; always confirms. Use after rebuilding the agent binary or to clear accumulated runtime state. Args: {reason?: string}."
}

func (restartAgentCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","properties":{"reason":{"type":"string","description":"Optional short reason for the restart, recorded in the agent log."}}}`)
}

func (restartAgentCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a struct {
		Reason string `json:"reason"`
	}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("restart_agent: parse args: %w", err)
		}
	}
	reason := strings.TrimSpace(a.Reason)
	if reason == "" {
		reason = "agent-requested restart"
	}
	if call.Svc.RestartAgent == nil {
		return nil, errors.New("restart_agent: agent restart is not available in this context")
	}
	if err := call.Svc.RestartAgent(reason); err != nil {
		return nil, fmt.Errorf("restart_agent: %w", err)
	}
	return capabilities.NewTextResult("agent restart scheduled (" + reason + ") — the connection will drop and the CLI will reconnect to a fresh agent momentarily."), nil
}
