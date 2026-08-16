package agent

import (
	"reflect"
	"testing"

	"cercano/source/server/internal/llm"
)

// conv is a stand-in conversation ID used throughout the broker tests.
const conv = "conv1"

func TestProfileBroker_DefaultsToUnrestricted(t *testing.T) {
	b := NewProfileBroker()
	if b.ActiveName(conv) != DefaultProfileName {
		t.Fatalf("ActiveName = %q, want %q", b.ActiveName(conv), DefaultProfileName)
	}
	if b.Active(conv).Restricts() {
		t.Fatal("default profile must not restrict")
	}
	// A write tool is allowed under the default (no fence).
	if !b.Active(conv).Allows(llm.PermW, "Write") {
		t.Fatal("default profile should allow all tools")
	}
}

func TestProfileBroker_SwitchToPlanAndBack(t *testing.T) {
	b := NewProfileBroker()

	if err := b.SetActive(conv, "plan"); err != nil {
		t.Fatalf("SetActive(plan): %v", err)
	}
	if b.ActiveName(conv) != "plan" {
		t.Fatalf("ActiveName = %q, want plan", b.ActiveName(conv))
	}
	p := b.Active(conv)
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
		if err := b.SetActive(conv, name); err != nil {
			t.Fatalf("SetActive(%q): %v", name, err)
		}
		if b.ActiveName(conv) != DefaultProfileName {
			t.Fatalf("after SetActive(%q), ActiveName = %q", name, b.ActiveName(conv))
		}
		if b.Active(conv).Restricts() {
			t.Fatalf("after SetActive(%q), profile should be unrestricted", name)
		}
	}
}

// TestProfileBroker_ActiveIsPerConversation is the regression guard for the bug
// this keying fixes: one conversation entering planning mode must NOT fence any
// other attached conversation.
func TestProfileBroker_SwitchToAutonomous(t *testing.T) {
	b := NewProfileBroker()

	if err := b.SetActive(conv, "autonomous"); err != nil {
		t.Fatalf("SetActive(autonomous): %v", err)
	}
	if b.ActiveName(conv) != "autonomous" {
		t.Fatalf("ActiveName = %q, want autonomous", b.ActiveName(conv))
	}
	p := b.Active(conv)
	if !p.Restricts() {
		t.Fatal("autonomous profile must signal as an active profile")
	}
	if !p.Allows(llm.PermX, "Bash") || !p.Allows(llm.PermW, "Write") || !p.Allows(llm.PermR, "Read") {
		t.Fatal("autonomous profile should not fence normal tool tiers")
	}
}

func TestProfileBroker_ActiveIsPerConversation(t *testing.T) {
	b := NewProfileBroker()
	const (
		alice = "conv-alice"
		bob   = "conv-bob"
	)

	// Alice enters planning mode.
	if err := b.SetActive(alice, "plan"); err != nil {
		t.Fatalf("SetActive(alice, plan): %v", err)
	}

	// Alice is fenced…
	if !b.Active(alice).Restricts() {
		t.Fatal("alice should be in the planning fence")
	}
	if b.ActiveName(alice) != "plan" {
		t.Fatalf("alice active = %q, want plan", b.ActiveName(alice))
	}
	// …but Bob is untouched.
	if b.Active(bob).Restricts() {
		t.Fatal("bob must NOT be fenced by alice entering planning mode")
	}
	if b.ActiveName(bob) != DefaultProfileName {
		t.Fatalf("bob active = %q, want default", b.ActiveName(bob))
	}

	// Alice leaving planning mode leaves Bob's (already default) state alone.
	if err := b.SetActive(alice, "default"); err != nil {
		t.Fatalf("SetActive(alice, default): %v", err)
	}
	if b.Active(alice).Restricts() || b.Active(bob).Restricts() {
		t.Fatal("both conversations should be unrestricted after alice exits")
	}
}

func TestProfileBroker_UnknownNameIsErrorAndLeavesActiveUnchanged(t *testing.T) {
	b := NewProfileBroker()
	if err := b.SetActive(conv, "plan"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetActive(conv, "nonexistent"); err == nil {
		t.Fatal("expected error for unknown profile")
	}
	// Active must be unchanged after the failed switch.
	if b.ActiveName(conv) != "plan" {
		t.Fatalf("failed switch changed active to %q; want it to stay plan", b.ActiveName(conv))
	}
}

func TestProfileBroker_RegisterRejectsReservedNames(t *testing.T) {
	b := NewProfileBroker()
	b.Register(Profile{Name: ""})
	b.Register(Profile{Name: DefaultProfileName, AllowedTiers: map[llm.Permission]bool{llm.PermR: true}})
	// "default" must never resolve to a fence.
	if err := b.SetActive(conv, "default"); err != nil {
		t.Fatal(err)
	}
	if b.Active(conv).Restricts() {
		t.Fatal("default must remain unrestricted even after a Register attempt")
	}
}

func TestProfileBroker_NamesSortedExcludesDefault(t *testing.T) {
	b := NewProfileBroker()
	b.Register(Profile{Name: "brainstorm", AllowedTiers: map[llm.Permission]bool{llm.PermR: true}})
	got := b.Names()
	want := []string{"autonomous", "brainstorm", "plan"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
}
