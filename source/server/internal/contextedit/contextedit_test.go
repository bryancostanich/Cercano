package contextedit

import (
	"context"
	"errors"
	"testing"
)

var sampleTurns = []TurnSummary{
	{ID: "a", Role: "user", Kind: "text", Preview: "let's debug the panic"},
	{ID: "b", Role: "assistant", Kind: "text", Preview: "the nil deref is in foo()"},
	{ID: "c", Role: "user", Kind: "text", Preview: "now design the API"},
}

func fixed(out string, err error) CompleteFunc {
	return func(context.Context, string) (string, error) { return out, err }
}

func TestPropose_ValidJSON(t *testing.T) {
	local := fixed(`{"delete_ids":["a","b"],"rationale":"removed the debugging tangent"}`, nil)
	p, err := Propose(context.Background(), "drop the debugging", sampleTurns, local, nil)
	if err != nil { t.Fatalf("Propose: %v", err) }
	if len(p.DeleteIDs) != 2 || p.DeleteIDs[0] != "a" || p.DeleteIDs[1] != "b" {
		t.Errorf("delete_ids = %v", p.DeleteIDs)
	}
	if p.Rationale == "" { t.Error("missing rationale") }
}

func TestPropose_DropsHallucinatedID(t *testing.T) {
	local := fixed(`{"delete_ids":["a","zzz"],"rationale":"x"}`, nil)
	p, err := Propose(context.Background(), "i", sampleTurns, local, nil)
	if err != nil { t.Fatalf("Propose: %v", err) }
	if len(p.DeleteIDs) != 1 || p.DeleteIDs[0] != "a" {
		t.Errorf("expected only real id [a], got %v", p.DeleteIDs)
	}
}

func TestPropose_JSONInProse(t *testing.T) {
	local := fixed("Sure! Here you go:\n{\"delete_ids\":[\"c\"],\"rationale\":\"y\"}\nDone.", nil)
	p, err := Propose(context.Background(), "i", sampleTurns, local, nil)
	if err != nil || len(p.DeleteIDs) != 1 || p.DeleteIDs[0] != "c" {
		t.Fatalf("prose-wrapped JSON not parsed: %v / %+v", err, p)
	}
}

func TestPropose_OpenFailsCloudFallback(t *testing.T) {
	local := fixed("", errors.New("local down"))
	cloud := fixed(`{"delete_ids":["a"],"rationale":"z"}`, nil)
	p, err := Propose(context.Background(), "i", sampleTurns, local, cloud)
	if err != nil || len(p.DeleteIDs) != 1 || p.DeleteIDs[0] != "a" {
		t.Fatalf("cloud fallback failed: %v / %+v", err, p)
	}
}

func TestPropose_AllUnparseable(t *testing.T) {
	local := fixed("no json here", nil)
	if _, err := Propose(context.Background(), "i", sampleTurns, local, nil); err == nil {
		t.Fatal("expected error when no parseable proposal")
	}
}
