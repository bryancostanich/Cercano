package research

import "testing"

func TestProgressTrackerNotifiesSemanticProgress(t *testing.T) {
	pt := NewProgressTracker("")
	var states []ProgressState
	pt.SetProgressFunc(func(state ProgressState) {
		states = append(states, state)
	})

	pt.StartPhase("Analyzing", 3)
	pt.SetStep("Finding 1 of 3: ATIF schema")
	pt.IncrementFindings()
	pt.CompleteItem()
	pt.Done(2, 1)

	if len(states) < 5 {
		t.Fatalf("expected progress callbacks, got %+v", states)
	}
	if states[0].Phase != "Analyzing" || states[0].Total != 3 {
		t.Fatalf("start state = %+v", states[0])
	}
	if states[1].Step != "Finding 1 of 3: ATIF schema" {
		t.Fatalf("step state = %+v", states[1])
	}
	last := states[len(states)-1]
	if last.Phase != "complete" || last.Step != "2 findings from 1 sources" || last.FindingsAccepted != 2 {
		t.Fatalf("done state = %+v", last)
	}
}
