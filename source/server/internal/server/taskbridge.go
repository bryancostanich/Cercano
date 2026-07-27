package server

import (
	runnersvc "cercano/source/server/internal/runner"
	"cercano/source/server/internal/taskmodel"
)

// taskChangeSink returns a taskmodel change sink that republishes every store
// mutation onto the given runner.EventSink as an EventTaskChange. This is the
// single publish seam the task-model design (§11) calls for: the store knows
// nothing about the wire or the broker; it just calls one injected func, and
// this bridge maps the proto-free ChangeEvent onto the same runner-event stream
// that token deltas, tool calls, and watchdog events already ride. Because the
// sink flows through a brokerSink, the turn broker's existing 1→N fan-out
// delivers the change to every client attached to the conversation.
//
// A nil sink yields a nil result — callers pass that straight to the store
// constructor, where a nil sink makes emission a no-op.
func taskChangeSink(sink runnersvc.EventSink) func(taskmodel.ChangeEvent) {
	if sink == nil {
		return nil
	}
	return func(ev taskmodel.ChangeEvent) {
		sink.Emit(runnersvc.Event{
			Kind:           runnersvc.EventTaskChange,
			TaskChangeKind: string(ev.Kind),
			TaskSnapshot:   taskToSnapshot(ev.Task),
		})
	}
}

// taskToSnapshot recursively converts a taskmodel.Task into the runner's
// proto-free TaskSnapshot, preserving child (document) order. The store already
// handed us a deep-copy snapshot, so this reads it freely.
func taskToSnapshot(t taskmodel.Task) runnersvc.TaskSnapshot {
	snap := runnersvc.TaskSnapshot{
		ID:     t.ID,
		Title:  t.Title,
		Status: string(t.Status),
		Notes:  t.Notes,
	}
	if t.ParentID != nil {
		snap.ParentID = *t.ParentID
	}
	if len(t.Children) > 0 {
		snap.Children = make([]runnersvc.TaskSnapshot, len(t.Children))
		for i := range t.Children {
			snap.Children[i] = taskToSnapshot(t.Children[i])
		}
	}
	return snap
}
