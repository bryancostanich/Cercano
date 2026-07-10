package uiconfig

import "testing"

func TestLastConversationRoundTrip(t *testing.T) {
	t.Setenv("CERCANO_SESSION_STATE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, ok := LoadLastConversation("/proj/a"); ok {
		t.Fatal("expected no saved session initially")
	}
	if err := SaveLastConversation("/proj/a", "conv-a"); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := SaveLastConversation("/proj/b", "conv-b"); err != nil {
		t.Fatalf("save b: %v", err)
	}
	if id, ok := LoadLastConversation("/proj/a"); !ok || id != "conv-a" {
		t.Fatalf("load a = %q,%v want conv-a,true", id, ok)
	}
	// Overwriting one project must not clobber another.
	if err := SaveLastConversation("/proj/a", "conv-a2"); err != nil {
		t.Fatalf("save a2: %v", err)
	}
	if id, _ := LoadLastConversation("/proj/a"); id != "conv-a2" {
		t.Fatalf("load a after overwrite = %q want conv-a2", id)
	}
	if id, ok := LoadLastConversation("/proj/b"); !ok || id != "conv-b" {
		t.Fatalf("b was clobbered: %q,%v", id, ok)
	}
	if _, ok := LoadLastConversation("/unknown"); ok {
		t.Fatal("unknown dir should return false")
	}
}
