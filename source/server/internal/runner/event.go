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
	EventRouteSelected    EventKind = iota // model badge (which provider/model served the turn)
	EventToken                             // assistant text delta
	EventProgress                          // status message
	EventToolUseStart                      // model planned a tool call
	EventToolUseStop                       // model finalized a tool call (with args)
	EventToolExecStart                     // tool execution began
	EventToolExecComplete                  // tool execution finished
	EventWatchdog                          // protocol-supervision event; WatchdogKind holds the flavor (challenge/block/echo/escalate)
	EventSubAgent                          // structured child-agent lifecycle/transcript event
	EventTaskChange                        // planning/session task-store mutation; TaskChange* fields set
	EventDone                              // turn complete; Result populated
)

// TaskSnapshot is the runner's proto-free mirror of a task-store node snapshot.
// It carries the affected subtree in significant (document) order so the host
// can map it to proto.TaskNode and the client can render it directly. Kept in
// this package so the runner event vocabulary stays free of taskmodel imports —
// the host constructs these from taskmodel.Task at the store→broker seam.
type TaskSnapshot struct {
	ID       string
	Title    string
	Status   string
	Notes    string
	ParentID string // "" for a root
	Children []TaskSnapshot
}

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

	// Task store: EventTaskChange. TaskChangeKind is "added"|"updated"|"removed";
	// TaskSnapshot is the affected subtree snapshot.
	TaskChangeKind string
	TaskSnapshot   TaskSnapshot

	// Sub-agent lifecycle/transcript: EventSubAgent.
	SubAgentID       string
	SubAgentParentID string
	SubAgentTitle    string
	SubAgentKind     string
	GrantedTools     []string
	IgnoredTools     []string

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
