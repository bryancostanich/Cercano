// Package activation performs the READ-ONLY reconciliation between a
// validated activation journal (the state package's durable record of
// activation intent) and the launcher-readable selection file.
//
// Scope discipline. This package PARSES and CLASSIFIES, nothing more. It
// never writes, switches, deletes, launches, stops, or probes anything, and
// it issues no commands. The next safe action it returns is a typed
// DECISION for a future executor slice to carry out; returning an action
// never performs it.
//
// Explicitness. This package has NO default or live selection location:
// the selection path is always caller-supplied and is used exactly as
// given, the way the state package treats its state root. It performs no
// environment lookups and never derives a path from an executable, version,
// pointer, or heuristic. Tests read only temporary directories they own.
//
// Trust model. The selection file and the journal are trusted inputs of a
// per-installation update flow; this package makes NO publisher-trust
// claim about the artifact the selection names — verification of artifact
// authenticity belongs to the verifying/staging slice that produced the
// journal's recorded digest. Everything here is same-user, per-installation
// consistency checking.
//
// Absent is a first-class observation, distinct from unreadable (exists but
// cannot be read safely) and from malformed (read but not a valid
// selection). Every one of those is a typed state; none is silently
// coerced into another.
package activation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"cercano/source/server/internal/updatecoord/state"
)

// SelectionSchemaVersion is the only selection schema version this package
// accepts. A bump is a deliberate, reviewed event.
const SelectionSchemaVersion = 1

// maxSelectionJSONBytes bounds the selection payload. Every field of a
// selection is itself bounded far below this, so a larger file is never a
// legitimate selection.
const maxSelectionJSONBytes = 4 * 1024

// Selection is the typed, validated launcher-readable selection descriptor:
// the installation it belongs to, the strictly-increasing generation counter
// of the selection writer, the selected version, the RELATIVE staged
// version identifier (never an absolute path), and the lowercase-hex
// SHA-256 of the verified artifact the selection points at.
//
// The JSON field names bind to the activation journal's fields:
// install_id, staged_version_dir, and verified_artifact_sha256 use the
// journal's exact names; selected_version is the selection-side spelling of
// the journal's target/prior selected version; generation is the selection
// writer's counter that the journal records for the prior selection.
type Selection struct {
	SchemaVersion          int    `json:"schema_version"`
	InstallID              string `json:"install_id"`
	Generation             int64  `json:"generation"`
	SelectedVersion        string `json:"selected_version"`
	StagedVersionDir       string `json:"staged_version_dir"`
	VerifiedArtifactSHA256 string `json:"verified_artifact_sha256"`
}

// Validate checks a decoded selection against the same identifier rules the
// activation journal applies: exact schema version, a safe opaque
// installation identifier, a positive generation (generation zero is the
// journal's explicit "no prior selection" marker and can never name a
// selected version), a bounded valid version string, a RELATIVE staged
// identifier, and a canonical lowercase-hex SHA-256 artifact digest.
func (s Selection) Validate() error {
	if s.SchemaVersion != SelectionSchemaVersion {
		return fmt.Errorf("selection schema version %d, want %d", s.SchemaVersion, SelectionSchemaVersion)
	}
	if err := state.ValidateInstallID(s.InstallID); err != nil {
		return fmt.Errorf("selection installation identifier: %w", err)
	}
	if s.Generation < 1 {
		return errors.New("selection generation must be positive")
	}
	if err := state.ValidateVersionString(s.SelectedVersion); err != nil {
		return fmt.Errorf("selected version: %w", err)
	}
	if err := state.ValidateStagedRelativeIdentifier(s.StagedVersionDir); err != nil {
		return fmt.Errorf("staged version directory: %w", err)
	}
	if !state.IsLowerHexSHA256(s.VerifiedArtifactSHA256) {
		return errors.New("selection artifact digest must be a 64-character lowercase hex SHA-256")
	}
	return nil
}

// ParseSelection strictly decodes and validates a selection payload:
// bounded, valid UTF-8, exactly one JSON object, no duplicate keys, no
// case-alias keys, no null values, no missing fields, no unknown fields, no
// trailing data, then full Validate. A corrupt payload is an error, never
// an empty selection.
func ParseSelection(data []byte) (Selection, error) {
	var s Selection
	if len(data) == 0 || len(data) > maxSelectionJSONBytes || !utf8.Valid(data) {
		return s, errors.New("selection payload empty, oversize, or invalid UTF-8")
	}
	// Selection fields are flat canonical names: reject duplicate/case
	// aliases before encoding/json can silently pick a last value, the
	// same discipline the state package applies to stored journals.
	scan := json.NewDecoder(bytes.NewReader(data))
	tok, err := scan.Token()
	if err != nil || tok != json.Delim('{') {
		return s, errors.New("selection payload is not a JSON object")
	}
	seen := map[string]bool{}
	requiredFields := map[string]bool{
		"schema_version":           true,
		"install_id":               true,
		"generation":               true,
		"selected_version":         true,
		"staged_version_dir":       true,
		"verified_artifact_sha256": true,
	}
	for scan.More() {
		tok, err = scan.Token()
		if err != nil {
			return s, fmt.Errorf("selection key: %w", err)
		}
		key, ok := tok.(string)
		if !ok || key == "" || key != strings.ToLower(key) || seen[key] {
			return s, fmt.Errorf("duplicate, empty, or non-canonical field name")
		}
		seen[key] = true
		delete(requiredFields, key)
		var value json.RawMessage
		if err = scan.Decode(&value); err != nil {
			return s, fmt.Errorf("selection field %q: %w", key, err)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return s, fmt.Errorf("null value not allowed for field %q", key)
		}
	}
	if len(requiredFields) > 0 {
		return s, errors.New("selection is missing required fields")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Selection{}, fmt.Errorf("malformed selection JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return Selection{}, fmt.Errorf("trailing data: %w", err)
		}
		return Selection{}, errors.New("trailing data")
	}
	if err := s.Validate(); err != nil {
		return Selection{}, err
	}
	return s, nil
}
