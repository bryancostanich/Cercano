package main

import (
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/loopcompact"
	"context"
	"testing"
	"time"
)

func TestCompactionSharedExecutionPolicy(t *testing.T) {
	if loopcompact.DefaultTimeout != compaction.ExecutionTimeout {
		t.Fatal("inline and main compaction budgets diverge")
	}
	if compaction.ExecutionTimeout != 6*time.Minute {
		t.Fatal("unexpected execution budget")
	}
	parent, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Second))
	defer cancel()
	child, done := compaction.WithExecutionBudget(parent)
	defer done()
	pd, _ := parent.Deadline()
	cd, _ := child.Deadline()
	if !cd.Equal(pd) {
		t.Fatal("extended caller deadline")
	}
	cancel()
	if child.Err() != context.Canceled {
		t.Fatal("detached from cancellation")
	}
}
