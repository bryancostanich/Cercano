package conversation

import (
	"context"
	"testing"
)

// TestEnsureSubagentConversation_LinksParent verifies the row is created with
// kind "subagent" and the parent conversation id, retrievable via Get.
func TestEnsureSubagentConversation_LinksParent(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "parent1", "/proj", "model-a"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.EnsureSubagentConversation(ctx, "child1", "parent1", "/proj", "model-a"); err != nil {
		t.Fatalf("EnsureSubagentConversation: %v", err)
	}

	info, err := store.Get(ctx, "child1")
	if err != nil {
		t.Fatalf("Get(child1): %v", err)
	}
	if info.Kind != "subagent" {
		t.Errorf("Kind = %q, want %q", info.Kind, "subagent")
	}
	if info.ParentID != "parent1" {
		t.Errorf("ParentID = %q, want %q", info.ParentID, "parent1")
	}
}

// TestList_ExcludesSubagentConversations verifies /history stays clean: List
// returns only main conversations; sub-agent loops are reachable by id.
func TestList_ExcludesSubagentConversations(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "main1", "/proj", "m"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.EnsureSubagentConversation(ctx, "sub1", "main1", "/proj", "m"); err != nil {
		t.Fatalf("EnsureSubagentConversation: %v", err)
	}

	infos, err := store.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, in := range infos {
		if in.ID == "sub1" {
			t.Fatalf("List included subagent conversation %q; want it hidden", in.ID)
		}
	}
	found := false
	for _, in := range infos {
		if in.ID == "main1" {
			found = true
		}
	}
	if !found {
		t.Errorf("List missing main conversation main1")
	}
}

// TestEnsureConversation_DefaultsToMainKind verifies existing behavior is
// unchanged: plain conversations carry kind "main" and no parent.
func TestEnsureConversation_DefaultsToMainKind(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "plain1", "/proj", "m"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	info, err := store.Get(ctx, "plain1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if info.Kind != "main" {
		t.Errorf("Kind = %q, want %q", info.Kind, "main")
	}
	if info.ParentID != "" {
		t.Errorf("ParentID = %q, want empty", info.ParentID)
	}
}

// TestNewID_Exported verifies the exported id minter returns unique non-empty ids
// (the server uses it to mint sub-conversation ids for dispatch loops).
func TestNewID_Exported(t *testing.T) {
	a, b := NewID(), NewID()
	if a == "" || b == "" {
		t.Fatal("NewID returned empty id")
	}
	if a == b {
		t.Fatalf("NewID returned duplicate ids %q", a)
	}
}
