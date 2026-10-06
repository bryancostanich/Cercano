package policy

import (
	"sync"
	"testing"
)

// These tests are pure: the dismissal store is an in-memory value holder used
// only by tests; no preference file, registry, or other persistence layer is
// touched.

func validDismissal() Dismissal {
	return Dismissal{
		InstallID: "install-a",
		Channel:   "stable",
		Source:    "tuf",
		Version:   "0.9.0",
	}
}

func TestDismissal_RoundTrip(t *testing.T) {
	d := validDismissal()
	data, err := d.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	decoded, err := DecodeDismissalRecord(data)
	if err != nil {
		t.Fatalf("DecodeDismissalRecord failed: %v", err)
	}
	if decoded != d {
		t.Fatalf("round trip mismatch:\nwant %+v\ngot  %+v", d, decoded)
	}
}

func TestDecodeDismissalRecord_RejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"empty-payload", ""},
		{"whitespace-only", "  \n "},
		{"malformed-json", `{"schema_version":1,`},
		{"json-array", `[]`},
		{"json-scalar", `true`},
		{"unknown-field", `{"schema_version":1,"extra":1}`},
		{"unknown-schema-version", `{"schema_version":2,"install_id":"a","channel":"stable","source":"tuf","version":"1"}`},
		{"zero-schema-version", `{"schema_version":0}`},
		{"wrong-type-schema-version", `{"schema_version":"1"}`},
		{"duplicate-key", `{"schema_version":1,"schema_version":1}`},
		{"empty-version", `{"schema_version":1,"install_id":"a","channel":"stable","source":"tuf","version":""}`},
		{"empty-install-id", `{"schema_version":1,"install_id":"","channel":"stable","source":"tuf","version":"1"}`},
		{"empty-channel", `{"schema_version":1,"install_id":"a","channel":"","source":"tuf","version":"1"}`},
		{"trailing-value", `{"schema_version":1,"install_id":"a","channel":"stable","source":"tuf","version":"1"} {"x":1}`},
		{"nul-escape-in-field", `{"schema_version":1,"install_id":"a\u0000","channel":"stable","source":"tuf","version":"1"}`},
		{"padded-install-id", `{"schema_version":1,"install_id":" a","channel":"stable","source":"tuf","version":"1"}`},
	}
	for _, tc := range cases {
		if _, err := DecodeDismissalRecord([]byte(tc.data)); err == nil {
			t.Errorf("%s: decode unexpectedly succeeded", tc.name)
		}
	}
}

func TestDismissalStore_IsDismissedRequiresExactBinding(t *testing.T) {
	store := NewDismissalStore()
	base := validDismissal()
	if err := store.Dismiss(base); err != nil {
		t.Fatalf("Dismiss failed: %v", err)
	}

	if !store.IsDismissed(base) {
		t.Fatal("exact binding not dismissed")
	}
	// Version-scoped: a different version is never suppressed.
	next := base
	next.Version = "0.10.0"
	if store.IsDismissed(next) {
		t.Fatal("dismissal leaked across versions")
	}
	prev := base
	prev.Version = "0.8.0"
	if store.IsDismissed(prev) {
		t.Fatal("dismissal leaked to an older version")
	}
	// Channel binding: a different release channel is never suppressed.
	beta := base
	beta.Channel = "beta"
	if store.IsDismissed(beta) {
		t.Fatal("dismissal leaked across release channels")
	}
	// Source binding: a different update source is never suppressed.
	choco := base
	choco.Source = "chocolatey"
	if store.IsDismissed(choco) {
		t.Fatal("dismissal leaked across sources")
	}
	// Install binding: another installation is never suppressed.
	other := base
	other.InstallID = "install-b"
	if store.IsDismissed(other) {
		t.Fatal("dismissal leaked across installations")
	}
	// No permissive "suppress everything" behavior: an empty binding must
	// not match the recorded dismissal.
	if store.IsDismissed(Dismissal{InstallID: "", Channel: "stable", Source: "tuf", Version: "0.9.0"}) {
		t.Fatal("empty install ID matched")
	}
	if store.IsDismissed(Dismissal{InstallID: "install-a", Channel: "", Source: "tuf", Version: "0.9.0"}) {
		t.Fatal("empty channel matched")
	}
	if store.IsDismissed(Dismissal{InstallID: "install-a", Channel: "stable", Source: "", Version: "0.9.0"}) {
		t.Fatal("empty source matched")
	}
	if store.IsDismissed(Dismissal{InstallID: "install-a", Channel: "stable", Source: "tuf", Version: ""}) {
		t.Fatal("empty version matched")
	}
}

func TestDismissalStore_RejectsInvalidDismissals(t *testing.T) {
	store := NewDismissalStore()
	cases := []Dismissal{
		{InstallID: "", Channel: "stable", Source: "tuf", Version: "1"},
		{InstallID: "a", Channel: "", Source: "tuf", Version: "1"},
		{InstallID: "a", Channel: "stable", Source: "", Version: "1"},
		{InstallID: "a", Channel: "stable", Source: "tuf", Version: ""},
		{InstallID: " a", Channel: "stable", Source: "tuf", Version: "1"},
	}
	for _, d := range cases {
		if err := store.Dismiss(d); err == nil {
			t.Errorf("Dismiss accepted invalid %+v", d)
		}
		if store.IsDismissed(d) {
			t.Errorf("invalid dismissal %+v answered true", d)
		}
	}
}

func TestDismissalStore_ConcurrentAccess(t *testing.T) {
	store := NewDismissalStore()
	base := validDismissal()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := store.Dismiss(base); err != nil {
				t.Errorf("Dismiss failed: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			store.IsDismissed(base)
		}()
	}
	wg.Wait()
	if !store.IsDismissed(base) {
		t.Fatal("dismissal lost after concurrent writes")
	}
}
