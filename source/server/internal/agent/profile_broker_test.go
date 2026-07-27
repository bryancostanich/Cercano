package agent

import (
	"reflect"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestProfileBroker_DefaultsToUnrestricted(t *testing.T) {
	b := NewProfileBroker()
	if b.ActiveName() != DefaultProfileName {
		t.Fatalf("ActiveName = %q, want %q", b.ActiveName(), DefaultProfileName)
	}
	if b.Active().Restricts() {
		t.Fatal("default profile must not restrict")
	}
	// A write tool is allowed under the default (no fence).
	if !b.Active().Allows(llm.PermW, "Write") {
		t.Fatal("default profile should allow all tools")
	}
}

func TestProfileBroker_SwitchToPlanAndBack(t *testing.T) {
	b := NewProfileBroker()

	if err := b.SetActive("plan"); err != nil {
		t.Fatalf("SetActive(plan): %v", err)
	}
	if b.ActiveName() != "plan" {
		t.Fatalf("ActiveName = %q, want plan", b.ActiveName())
	}
	p := b.Active()
	if !p.Restricts() {
		t.Fatal("plan profile must restrict")
	}
	// The fence bites: exec fenced, file-write allowed, read allowed.
	if p.Allows(llm.PermX, "Bash") {
		t.Fatal("plan profile must fence Bash")
	}
	if !p.Allows(llm.PermW, "Write") {
		t.Fatal("plan profile must allow Write")
	}

	// Back to default via each accepted spelling.
	for _, name := range []string{"", "default"} {
		if err := b.SetActive(name); err != nil {
			t.Fatalf("SetActive(%q): %v", name, err)
		}
		if b.ActiveName() != DefaultProfileName {
			t.Fatalf("after SetActive(%q), ActiveName = %q", name, b.ActiveName())
		}
		if b.Active().Restricts() {
			t.Fatalf("after SetActive(%q), profile should be unrestricted", name)
		}
	}
}

func TestProfileBroker_UnknownNameIsErrorAndLeavesActiveUnchanged(t *testing.T) {
	b := NewProfileBroker()
	if err := b.SetActive("plan"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetActive("nonexistent"); err == nil {
		t.Fatal("expected error for unknown profile")
	}
	// Active must be unchanged after the failed switch.
	if b.ActiveName() != "plan" {
		t.Fatalf("failed switch changed active to %q; want it to stay plan", b.ActiveName())
	}
}

func TestProfileBroker_RegisterRejectsReservedNames(t *testing.T) {
	b := NewProfileBroker()
	b.Register(Profile{Name: ""})
	b.Register(Profile{Name: DefaultProfileName, AllowedTiers: map[llm.Permission]bool{llm.PermR: true}})
	// "default" must never resolve to a fence.
	if err := b.SetActive("default"); err != nil {
		t.Fatal(err)
	}
	if b.Active().Restricts() {
		t.Fatal("default must remain unrestricted even after a Register attempt")
	}
}

func TestProfileBroker_NamesSortedExcludesDefault(t *testing.T) {
	b := NewProfileBroker()
	b.Register(Profile{Name: "brainstorm", AllowedTiers: map[llm.Permission]bool{llm.PermR: true}})
	got := b.Names()
	want := []string{"brainstorm", "plan"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
}
