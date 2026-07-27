package server

import (
	"testing"

	runnersvc "cercano/source/server/internal/runner"
	"cercano/source/server/internal/taskmodel"
	"cercano/source/server/pkg/proto"
)

// capSink captures runner events emitted through the EventSink interface, so we
// can assert what the task bridge publishes onto the broker stream.
type capSink struct{ events []runnersvc.Event }

func (c *capSink) Emit(ev runnersvc.Event) { c.events = append(c.events, ev) }

// sliceStream captures proto responses sent by sendRunnerEvent.
type sliceStream struct {
	sent []*proto.StreamProcessResponse
}

func (s *sliceStream) Send(m *proto.StreamProcessResponse) error {
	s.sent = append(s.sent, m)
	return nil
}

// TestTaskBridge_NilSink verifies a nil runner sink yields a nil store sink,
// which the store treats as no-op emission.
func TestTaskBridge_NilSink(t *testing.T) {
	if taskChangeSink(nil) != nil {
		t.Fatal("nil runner sink must yield a nil store sink")
	}
}

// TestTaskBridge_EndToEnd is the load-bearing proof of the wiring: a real store
// mutation flows store → taskChangeSink → runner.Event → sendRunnerEvent → proto,
// and a nested subtree (phase with sub-tasks, added in one shot) survives the
// whole path with structure and status intact.
func TestTaskBridge_EndToEnd(t *testing.T) {
	cap := &capSink{}
	store := taskmodel.NewSessionStore(taskChangeSink(cap))

	// Add a phase root carrying two sub-tasks in one Add — the store emits ONE
	// ChangeAdded for the whole subtree (not one per descendant).
	pid := "phase1"
	phase := taskmodel.Task{
		ID: "phase1", Title: "Phase 1", Status: taskmodel.StatusInProgress,
		Notes: "objective: wire it up",
		Children: []taskmodel.Task{
			{ID: "t1", Title: "first", Status: taskmodel.StatusPending, ParentID: &pid},
			{ID: "t2", Title: "second", Status: taskmodel.StatusDone, ParentID: &pid},
		},
	}
	if err := store.Add(phase); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if len(cap.events) != 1 {
		t.Fatalf("expected 1 runner event, got %d", len(cap.events))
	}
	ev := cap.events[0]
	if ev.Kind != runnersvc.EventTaskChange || ev.TaskChangeKind != "added" {
		t.Fatalf("event = %+v, want EventTaskChange/added", ev)
	}

	// Map the runner event onto the proto stream and inspect the wire node.
	stream := &sliceStream{}
	if err := sendRunnerEvent(stream, ev); err != nil {
		t.Fatalf("sendRunnerEvent: %v", err)
	}
	if len(stream.sent) != 1 {
		t.Fatalf("expected 1 proto response, got %d", len(stream.sent))
	}
	tc := stream.sent[0].GetTaskChange()
	if tc == nil {
		t.Fatalf("payload is not a TaskChange: %+v", stream.sent[0])
	}
	if tc.GetKind() != "added" {
		t.Fatalf("kind = %q, want added", tc.GetKind())
	}
	node := tc.GetTask()
	if node.GetId() != "phase1" || node.GetTitle() != "Phase 1" ||
		node.GetStatus() != "in_progress" || node.GetNotes() != "objective: wire it up" {
		t.Fatalf("root node mangled: %+v", node)
	}
	if len(node.GetChildren()) != 2 {
		t.Fatalf("children = %d, want 2", len(node.GetChildren()))
	}
	// Order preserved, parent back-refs set, statuses intact.
	c0, c1 := node.GetChildren()[0], node.GetChildren()[1]
	if c0.GetId() != "t1" || c0.GetStatus() != "pending" || c0.GetParentId() != "phase1" {
		t.Fatalf("child 0 mangled: %+v", c0)
	}
	if c1.GetId() != "t2" || c1.GetStatus() != "done" || c1.GetParentId() != "phase1" {
		t.Fatalf("child 1 mangled: %+v", c1)
	}
}

// TestTaskBridge_StatusFlipMapsUpdated confirms a SetStatus hot-path mutation
// maps to a TaskChange/updated on the wire.
func TestTaskBridge_StatusFlipMapsUpdated(t *testing.T) {
	cap := &capSink{}
	store := taskmodel.NewSessionStore(taskChangeSink(cap))
	if err := store.Add(taskmodel.Task{ID: "a", Title: "A", Status: taskmodel.StatusPending}); err != nil {
		t.Fatal(err)
	}
	cap.events = nil // drop the add
	if err := store.SetStatus("a", taskmodel.StatusDone); err != nil {
		t.Fatal(err)
	}
	if len(cap.events) != 1 || cap.events[0].TaskChangeKind != "updated" {
		t.Fatalf("events = %+v, want one updated", cap.events)
	}
	stream := &sliceStream{}
	if err := sendRunnerEvent(stream, cap.events[0]); err != nil {
		t.Fatal(err)
	}
	tc := stream.sent[0].GetTaskChange()
	if tc.GetKind() != "updated" || tc.GetTask().GetStatus() != "done" {
		t.Fatalf("wire = %+v, want updated/done", tc)
	}
}
