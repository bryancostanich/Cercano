package activation

import (
	"strings"
	"testing"
)

// validSelectionJSON is a canonical, valid selection payload. Fields bind to
// the journal's vocabulary: install_id, staged_version_dir, and
// verified_artifact_sha256 are the journal's exact field names.
var validSelectionJSON = `{
	"schema_version": 1,
	"install_id": "test-install",
	"generation": 4,
	"selected_version": "9.9.9",
	"staged_version_dir": "staged-9.9.9",
	"verified_artifact_sha256": "` + strings.Repeat("a", 64) + `"
}`

func TestParseSelectionValid(t *testing.T) {
	s, err := ParseSelection([]byte(validSelectionJSON))
	if err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion != 1 || s.InstallID != "test-install" || s.Generation != 4 ||
		s.SelectedVersion != "9.9.9" || s.StagedVersionDir != "staged-9.9.9" ||
		s.VerifiedArtifactSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("decoded selection mismatch: %+v", s)
	}
}

func TestParseSelectionStrict(t *testing.T) {
	digest := strings.Repeat("a", 64)
	cases := map[string]struct {
		payload string
		wantErr bool // every case must fail; wantErr documents intent
	}{
		"not an object":          {`[]`, true},
		"empty":                  {``, true},
		"only whitespace":        {`   `, true},
		"null literal":           {`null`, true},
		"duplicate key":          {`{"schema_version":1,"schema_version":1}`, true},
		"case-alias key":         {`{"Schema_Version":1}`, true},
		"unknown field":          {strings.Replace(validSelectionJSON, `"generation": 4,`, `"generation": 4, "extra": true,`, 1), true},
		"missing field":          {strings.Replace(validSelectionJSON, `"selected_version": "9.9.9",`, ``, 1), true},
		"null field value":       {strings.Replace(validSelectionJSON, `"generation": 4,`, `"generation": null,`, 1), true},
		"trailing data":          {validSelectionJSON + ` {}`, true},
		"truncated object":       {`{"schema_version":1`, true},
		"wrong schema version":   {strings.Replace(validSelectionJSON, `"schema_version": 1,`, `"schema_version": 2,`, 1), true},
		"generation zero":        {strings.Replace(validSelectionJSON, `"generation": 4,`, `"generation": 0,`, 1), true},
		"generation negative":    {strings.Replace(validSelectionJSON, `"generation": 4,`, `"generation": -1,`, 1), true},
		"generation fractional":  {strings.Replace(validSelectionJSON, `"generation": 4,`, `"generation": 4.5,`, 1), true},
		"generation as string":   {strings.Replace(validSelectionJSON, `"generation": 4,`, `"generation": "4",`, 1), true},
		"install id unsafe":      {strings.Replace(validSelectionJSON, `"test-install"`, `"../escape"`, 1), true},
		"install id empty":       {strings.Replace(validSelectionJSON, `"install_id": "test-install"`, `"install_id": ""`, 1), true},
		"selected version empty": {strings.Replace(validSelectionJSON, `"selected_version": "9.9.9"`, `"selected_version": " "`, 1), true},
		"staged dir absolute":    {strings.Replace(validSelectionJSON, `"staged-9.9.9"`, `"/opt/9.9.9"`, 1), true},
		"staged dir parent hop":  {strings.Replace(validSelectionJSON, `"staged-9.9.9"`, `"../9.9.9"`, 1), true},
		"staged dir backslash":   {strings.Replace(validSelectionJSON, `"staged-9.9.9"`, `versions\9.9.9`, 1), true},
		"digest short":           {strings.Replace(validSelectionJSON, digest, strings.Repeat("a", 63), 1), true},
		"digest uppercase":       {strings.Replace(validSelectionJSON, digest, strings.Repeat("A", 64), 1), true},
		"digest nonhex":          {strings.Replace(validSelectionJSON, digest, strings.Repeat("g", 64), 1), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if s, err := ParseSelection([]byte(tc.payload)); err == nil {
				t.Fatalf("invalid payload accepted: %+v", s)
			}
		})
	}
}

func TestParseSelectionBoundsAndUTF8(t *testing.T) {
	// Oversize: a payload larger than the bound is never a selection.
	if _, err := ParseSelection(make([]byte, maxSelectionJSONBytes+1)); err == nil {
		t.Fatal("oversize payload accepted")
	}
	// Exactly at the bound is still parsed (here: invalid UTF-8-free
	// garbage, so it must fail as malformed JSON, not as a bound breach).
	if _, err := ParseSelection(make([]byte, maxSelectionJSONBytes)); err == nil {
		t.Fatal("garbage payload accepted")
	}
	// Invalid UTF-8 inside an otherwise well-formed object is refused.
	bad := []byte(validSelectionJSON)
	bad[len(bad)-3] = 0xff
	if _, err := ParseSelection(bad); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestSelectionValidateRejectsStructValues(t *testing.T) {
	base := Selection{
		SchemaVersion:          1,
		InstallID:              "test-install",
		Generation:             4,
		SelectedVersion:        "9.9.9",
		StagedVersionDir:       "staged-9.9.9",
		VerifiedArtifactSHA256: strings.Repeat("a", 64),
	}
	mutations := map[string]func(*Selection){
		"schema version":   func(s *Selection) { s.SchemaVersion = 2 },
		"install id":       func(s *Selection) { s.InstallID = "Other-Install" },
		"generation":       func(s *Selection) { s.Generation = 0 },
		"selected version": func(s *Selection) { s.SelectedVersion = "" },
		"staged dir":       func(s *Selection) { s.StagedVersionDir = "C:\\x" },
		"digest":           func(s *Selection) { s.VerifiedArtifactSHA256 = strings.Repeat("0", 63) },
	}
	for name, mutate := range mutations {
		s := base
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Fatalf("%s mutation accepted", name)
		}
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid selection rejected: %v", err)
	}
}
