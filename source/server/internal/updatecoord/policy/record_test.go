package policy

import (
	"encoding/json"
	"strings"
	"testing"
)

// These tests are pure: they exercise in-memory record serialization only.
// Nothing is written to disk, and no production keys, feeds, or agents are
// involved.

func validDelegationRecord() DelegationRecord {
	return DelegationRecord{
		SchemaVersion:        DelegationSchemaVersion,
		InstallID:            "install-a",
		CanonicalRoot:        `C:\Users\me\AppData\Local\Cercano`,
		Owner:                "chocolatey",
		Platform:             "windows",
		Arch:                 "amd64",
		Scope:                "user",
		ReleaseChannel:       "stable",
		Source:               "tuf",
		FeedID:               "feed-stable-main",
		ConsentRecorded:      true,
		RegistrationRetained: true,
		ContractVersion:      "1",
	}
}

func TestDelegationRecord_RoundTrip(t *testing.T) {
	rec := validDelegationRecord()
	data, err := rec.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	decoded, err := DecodeDelegationRecord(data)
	if err != nil {
		t.Fatalf("DecodeDelegationRecord failed: %v", err)
	}
	if decoded != rec {
		t.Fatalf("round trip mismatch:\nwant %+v\ngot  %+v", rec, decoded)
	}
}

func TestDelegationRecord_EncodeStampsNewAndRefusesUnknownSchemaVersion(t *testing.T) {
	// Zero schema version means "new record": Encode stamps the current
	// version, then validates.
	rec := validDelegationRecord()
	rec.SchemaVersion = 0
	data, err := rec.Encode()
	if err != nil {
		t.Fatalf("Encode of new (zero-schema) record failed: %v", err)
	}
	decoded, err := DecodeDelegationRecord(data)
	if err != nil {
		t.Fatalf("decode of stamped encode failed: %v", err)
	}
	if decoded.SchemaVersion != DelegationSchemaVersion {
		t.Fatalf("schema version = %d, want %d", decoded.SchemaVersion, DelegationSchemaVersion)
	}

	// A NONZERO unknown schema version must NOT be silently coerced into
	// the current one: Encode refuses instead.
	unknown := validDelegationRecord()
	unknown.SchemaVersion = 99
	if data, err := unknown.Encode(); err == nil {
		t.Fatalf("Encode silently coerced unknown schema version %d into current %d (bytes: %s)", unknown.SchemaVersion, DelegationSchemaVersion, data)
	}
	// The current schema version still encodes.
	current := validDelegationRecord()
	if _, err := current.Encode(); err != nil {
		t.Fatalf("Encode of current-schema record failed: %v", err)
	}

	invalid := validDelegationRecord()
	invalid.InstallID = ""
	if _, err := invalid.Encode(); err == nil {
		t.Fatal("Encode accepted an invalid record")
	}
}

func TestDecodeDelegationRecord_RejectsMalformedInput(t *testing.T) {
	rec := validDelegationRecord()
	validJSON, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	cases := []struct {
		name string
		data string
	}{
		{"empty-payload", ""},
		{"whitespace-only", "   \n\t "},
		{"malformed-json", `{"schema_version":1,`},
		{"json-array", `[1,2,3]`},
		{"json-scalar", `42`},
		{"unknown-field", `{"schema_version":1,"extra":true}`},
		{"unknown-schema-version", `{"schema_version":2,"install_id":"a","canonical_root":"r","owner":"chocolatey","platform":"windows","arch":"amd64","scope":"user","release_channel":"stable","source":"tuf","consent_recorded":true,"registration_retained":true,"contract_version":"1"}`},
		{"zero-schema-version", `{"schema_version":0}`},
		{"wrong-type-schema-version", `{"schema_version":"1"}`},
		{"wrong-type-bool", `{"schema_version":1,"consent_recorded":"yes"}`},
		{"duplicate-key", `{"schema_version":1,"schema_version":1}`},
		{"trailing-value", string(validJSON) + ` {"more":true}`},
		{"truncated-after-value", strings.TrimSuffix(string(validJSON), "}")},
	}
	for _, tc := range cases {
		if _, err := DecodeDelegationRecord([]byte(tc.data)); err == nil {
			t.Errorf("%s: decode unexpectedly succeeded", tc.name)
		}
	}
}

func TestDecodeDelegationRecord_RejectsInvalidFields(t *testing.T) {
	mutate := func(field, value string) string {
		rec := validDelegationRecord()
		switch field {
		case "install_id":
			rec.InstallID = value
		case "canonical_root":
			rec.CanonicalRoot = value
		case "owner":
			rec.Owner = value
		case "platform":
			rec.Platform = value
		case "arch":
			rec.Arch = value
		case "scope":
			rec.Scope = value
		case "release_channel":
			rec.ReleaseChannel = value
		case "source":
			rec.Source = value
		case "feed_id":
			rec.FeedID = value
		case "contract_version":
			rec.ContractVersion = value
		}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(data)
	}
	cases := []struct{ field, value string }{
		{"install_id", ""},
		{"canonical_root", ""},
		{"owner", "winget"},
		{"scope", "global"},
		{"source", "github"},
		{"platform", ""},
		{"arch", ""},
		{"release_channel", ""},
		{"feed_id", ""},
		{"contract_version", ""},
		{"install_id", " install-a"},
		{"install_id", "install-a "},
	}
	for _, tc := range cases {
		if _, err := DecodeDelegationRecord([]byte(mutate(tc.field, tc.value))); err == nil {
			t.Errorf("field %s = %q: decode unexpectedly succeeded", tc.field, tc.value)
		}
	}

	// An embedded NUL delivered as a JSON \u0000 escape in the payload is
	// rejected by field validation.
	data := strings.Replace(mutate("install_id", "placeholder"), `"placeholder"`, `"install-a\u0000"`, 1)
	if _, err := DecodeDelegationRecord([]byte(data)); err == nil {
		t.Error("NUL-containing field accepted")
	}
}
