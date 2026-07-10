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
	if err := store.EnsureSubagentConversation(ctx, "child1", "parent1", "/proj", "model-a", nil); err != nil {
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
	if err := store.EnsureSubagentConversation(ctx, "sub1", "main1", "/proj", "m", nil); err != nil {
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

// TestListChildren_GrantedToolsRoundTrip verifies ListChildren returns only the
// given parent's sub-agent conversations, with the granted toolset persisted
// and restored (empty grant round-trips to nil).
func TestListChildren_GrantedToolsRoundTrip(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "main1", "/proj", "m"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.EnsureSubagentConversation(ctx, "sub1", "main1", "/proj", "m", []string{"Read", "Grep"}); err != nil {
		t.Fatalf("EnsureSubagentConversation sub1: %v", err)
	}
	if err := store.EnsureSubagentConversation(ctx, "sub2", "main1", "/proj", "m", nil); err != nil {
		t.Fatalf("EnsureSubagentConversation sub2: %v", err)
	}
	// A sub-agent under a different parent must not leak into main1's children.
	if err := store.EnsureSubagentConversation(ctx, "other", "mainX", "/proj", "m", []string{"LS"}); err != nil {
		t.Fatalf("EnsureSubagentConversation other: %v", err)
	}

	children, err := store.ListChildren(ctx, "main1")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("ListChildren returned %d, want 2: %+v", len(children), children)
	}
	byID := map[string]Info{}
	for _, c := range children {
		byID[c.ID] = c
	}
	if got := byID["sub1"].GrantedTools; len(got) != 2 || got[0] != "Read" || got[1] != "Grep" {
		t.Errorf("sub1 GrantedTools = %v, want [Read Grep]", got)
	}
	if got := byID["sub2"].GrantedTools; got != nil {
		t.Errorf("sub2 GrantedTools = %v, want nil", got)
	}
	if _, ok := byID["other"]; ok {
		t.Error("ListChildren leaked a sub-agent from a different parent")
	}
}

// TestListChildren_EmptyForNoChildren verifies a childless conversation yields
// no rows (and no error).
func TestListChildren_EmptyForNoChildren(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "lonely", "/proj", "m"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	children, err := store.ListChildren(ctx, "lonely")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("ListChildren = %d rows, want 0", len(children))
	}
}
