package conversation

import (
	"context"
	"testing"
)

// TestMarkSubagentDismissed_ExcludedFromListChildren verifies a dismissed
// sub-agent no longer surfaces via ListChildren — so a resumed CLI won't reopen
// its tab — while its siblings and the dismissed row itself remain intact.
func TestMarkSubagentDismissed_ExcludedFromListChildren(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "main1", "/proj", "m"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.EnsureSubagentConversation(ctx, "sub1", "main1", "/proj", "m", nil); err != nil {
		t.Fatalf("EnsureSubagentConversation sub1: %v", err)
	}
	if err := store.EnsureSubagentConversation(ctx, "sub2", "main1", "/proj", "m", nil); err != nil {
		t.Fatalf("EnsureSubagentConversation sub2: %v", err)
	}

	// Both children visible before dismissal.
	if children, err := store.ListChildren(ctx, "main1"); err != nil || len(children) != 2 {
		t.Fatalf("pre-dismiss ListChildren = %d (err %v), want 2", len(children), err)
	}

	if err := store.MarkSubagentDismissed(ctx, "sub1"); err != nil {
		t.Fatalf("MarkSubagentDismissed: %v", err)
	}

	children, err := store.ListChildren(ctx, "main1")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(children) != 1 || children[0].ID != "sub2" {
		t.Fatalf("post-dismiss ListChildren = %+v, want only sub2", children)
	}
	// The dismissed row still exists (its transcript is preserved); it's just
	// hidden from restore.
	if _, err := store.Get(ctx, "sub1"); err != nil {
		t.Errorf("Get(sub1) after dismiss: %v (the row should still exist)", err)
	}
	// Idempotent — dismissing again is fine.
	if err := store.MarkSubagentDismissed(ctx, "sub1"); err != nil {
		t.Errorf("second MarkSubagentDismissed: %v", err)
	}
}

// TestMarkSubagentDismissed_GuardsMainKind verifies the kind='subagent' guard:
// calling it on a main conversation is a harmless no-op and never errors.
func TestMarkSubagentDismissed_GuardsMainKind(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "main1", "/proj", "m"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.MarkSubagentDismissed(ctx, "main1"); err != nil {
		t.Fatalf("MarkSubagentDismissed(main1): %v", err)
	}
	// A main conversation with a child still lists that child afterward — the
	// guard didn't touch anything it shouldn't.
	if err := store.EnsureSubagentConversation(ctx, "sub1", "main1", "/proj", "m", nil); err != nil {
		t.Fatalf("EnsureSubagentConversation: %v", err)
	}
	children, err := store.ListChildren(ctx, "main1")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("ListChildren = %d, want 1", len(children))
	}
}
