package runner

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/llm"
)

// EventKind enumerates the runner's proto-free event vocabulary. The host maps
// these to proto.StreamProcessResponse payloads; embedded mode consumes them
// directly. Mirrors the pre-Phase-3 sink closure's cases.
type EventKind int

const (
	EventRouteSelected   EventKind = iota // model badge (which provider/model served the turn)
	EventToken                            // assistant text delta
	EventProgress                         // status message
	EventToolUseStart                     // model planned a tool call
	EventToolUseStop                      // model finalized a tool call (with args)
	EventToolExecStart                    // tool execution began
	EventToolExecComplete                 // tool execution finished
	EventWatchdog                         // protocol-supervision event; WatchdogKind holds the flavor (challenge/block/echo/escalate)
	EventDone                             // turn complete; Result populated
)

// Event is one runner-emitted notification. Only the fields relevant to Kind
// are set. Proto-free by design.
type Event struct {
	Kind EventKind

	// Text: EventToken text delta; EventProgress message.
	Text string

	// Route: EventRouteSelected.
	Model   string
	IsCloud bool

	// Tool lifecycle: EventToolUse*/EventToolExec*.
	ToolUseID   string
	ToolName    string
	ArgsSummary string
	Detail      string
	Summary     string
	StartLine   int
	IsError     bool

	// Watchdog: EventWatchdog. WatchdogKind is one of challenge/block/echo/escalate.
	WatchdogKind string
	Thread       string

	// EventDone.
	Result Result
	Notice string
}

// EventSink receives runner events as the turn runs. The in-process host
// implements it by mapping each Event to stream.Send(proto...); a worker
// serializes them over its bidi stream.
type EventSink interface {
	Emit(ev Event)
}

// PermissionRequester gates a W/X tool call: the runner asks, the host prompts
// the client and blocks until a decision (or ctx cancel). Separate from
// EventSink because it is request/response, not fire-and-forget. In-process
// this wraps the permission broker; a worker round-trips it over its stream.
type PermissionRequester func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (allow bool, err error)

// PersistFunc persists one turn message. The host fences it by turn generation
// (a superseded turn's writes are dropped), so the runner calls it blind.
// Nil-safe: nil means persistence disabled for this turn.
type PersistFunc func(m llm.Message)
