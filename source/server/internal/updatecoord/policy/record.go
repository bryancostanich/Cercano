package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"cercano/source/server/internal/updatecoord/installation"
)

// DelegationSchemaVersion is the current delegation record schema version.
// Records with any other schema version are rejected on decode.
const DelegationSchemaVersion = 1

// DelegationRecord is the persisted form of an explicit, consented
// delegation of the self-update role for one package-manager-owned
// installation, per the approved Chocolatey ownership contract. The record
// is DATA only: no field grants execution authority, and a positive decision
// requires EvaluateDelegation against distinct current evidence. Decode is
// security-critical and strict (see DecodeDelegationRecord).
type DelegationRecord struct {
	// SchemaVersion must equal DelegationSchemaVersion.
	SchemaVersion int `json:"schema_version"`
	// InstallID is the opaque installation identifier from the trusted
	// resolver, bound at enrollment.
	InstallID string `json:"install_id"`
	// CanonicalRoot is the canonical resolved installation root the
	// delegation is bound to.
	CanonicalRoot string `json:"canonical_root"`
	// Owner is the package manager that retains ownership (and therefore
	// uninstallation) of the installation.
	Owner string `json:"owner"`
	// Platform is the platform bound at enrollment. The approved contract
	// is Windows-only.
	Platform string `json:"platform"`
	// Arch is the architecture bound at enrollment.
	Arch string `json:"arch"`
	// Scope is the installation scope bound at enrollment. The approved
	// contract is user-scope-only.
	Scope string `json:"scope"`
	// ReleaseChannel is the release channel the delegated self-updates
	// consume.
	ReleaseChannel string `json:"release_channel"`
	// Source is the delegated update source. Delegated self-updates consume
	// the verified TUF feed only.
	Source string `json:"source"`
	// FeedID is the stable opaque identifier of the specific update feed
	// this installation consumes delegated updates from. The source kind
	// "tuf" names the trust model, not a feed: without this binding, a
	// record corroborated against one feed would authorize updates from
	// any other feed of the same kind. It must be nonempty and must match
	// the currently observed feed identity.
	FeedID string `json:"feed_id"`
	// ConsentRecorded records explicit user consent to this delegation at
	// enrollment. It is corroboration data: an unbound, revoked, or
	// unsupported record stays refused regardless.
	ConsentRecorded bool `json:"consent_recorded"`
	// RegistrationRetained records that the package-manager registration
	// is retained (Chocolatey remains the uninstall owner).
	RegistrationRetained bool `json:"registration_retained"`
	// ContractVersion is the cooperating-installer contract version bound
	// at enrollment. A fresh probe must corroborate it.
	ContractVersion string `json:"contract_version"`
	// Revoked records explicit revocation of the delegation.
	Revoked bool `json:"revoked"`
}

// Encode serializes the record after structural validation. An unset (zero)
// schema version is deliberately stamped with the current version — the
// caller built a NEW record — but an unknown nonzero schema version is
// refused instead of silently coerced into the current one. It writes
// nothing anywhere; the caller owns any persistence decision.
func (r DelegationRecord) Encode() ([]byte, error) {
	switch r.SchemaVersion {
	case 0, DelegationSchemaVersion:
		r.SchemaVersion = DelegationSchemaVersion
	default:
		return nil, fmt.Errorf("delegation record: refusing to coerce unknown schema version %d into current %d", r.SchemaVersion, DelegationSchemaVersion)
	}
	if err := r.validate(); err != nil {
		return nil, fmt.Errorf("delegation record: %w", err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("delegation record: marshal: %w", err)
	}
	return data, nil
}

// DecodeDelegationRecord strictly decodes and validates a delegation record.
// The decode is security-critical: unknown fields, unknown schema versions,
// duplicate keys, trailing data, malformed JSON, and invalid or empty field
// values are all rejected. There is no permissive mode.
func DecodeDelegationRecord(data []byte) (DelegationRecord, error) {
	var rec DelegationRecord
	if err := strictDecodeJSON(data, &rec); err != nil {
		return DelegationRecord{}, fmt.Errorf("delegation record: %w", err)
	}
	if rec.SchemaVersion != DelegationSchemaVersion {
		return DelegationRecord{}, fmt.Errorf("delegation record: unknown schema version %d, want %d", rec.SchemaVersion, DelegationSchemaVersion)
	}
	if err := rec.validate(); err != nil {
		return DelegationRecord{}, fmt.Errorf("delegation record: %w", err)
	}
	return rec, nil
}

// validate performs structural validation. It does not evaluate policy and
// grants nothing; EvaluateDelegation re-runs it defensively. An unknown
// nonzero schema version is a structural error too: records delivered as
// direct structs (bypassing DecodeDelegationRecord) must not carry a schema
// the current code does not know. Zero means "unset/new" and is accepted so
// in-process construction stays simple.
func (r DelegationRecord) validate() error {
	if r.SchemaVersion != 0 && r.SchemaVersion != DelegationSchemaVersion {
		return fmt.Errorf("schema_version: unknown schema version %d, want %d", r.SchemaVersion, DelegationSchemaVersion)
	}
	for field, value := range map[string]string{
		"install_id":       r.InstallID,
		"canonical_root":   r.CanonicalRoot,
		"owner":            r.Owner,
		"platform":         r.Platform,
		"arch":             r.Arch,
		"scope":            r.Scope,
		"release_channel":  r.ReleaseChannel,
		"source":           r.Source,
		"feed_id":          r.FeedID,
		"contract_version": r.ContractVersion,
	} {
		if err := validFieldToken(value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	if !knownOwnerValue(r.Owner) {
		return fmt.Errorf("owner: unknown owner %q", r.Owner)
	}
	if !knownScopeValue(r.Scope) {
		return fmt.Errorf("scope: unknown scope %q", r.Scope)
	}
	if r.Source != installation.DelegatedUpdateSource {
		return fmt.Errorf("source: delegated updates consume only %q, got %q", installation.DelegatedUpdateSource, r.Source)
	}
	return nil
}

// knownOwnerValue reports whether the owner string names a defined
// installation owner.
func knownOwnerValue(v string) bool {
	switch installation.Owner(v) {
	case installation.OwnerUnknown,
		installation.OwnerDevelopment,
		installation.OwnerHomebrew,
		installation.OwnerAPT,
		installation.OwnerChocolatey,
		installation.OwnerSelfManaged:
		return true
	}
	return false
}

// knownScopeValue reports whether the scope string names a defined
// installation scope.
func knownScopeValue(v string) bool {
	switch installation.Scope(v) {
	case installation.ScopeUser, installation.ScopeMachine, installation.ScopeUnknown:
		return true
	}
	return false
}

// validFieldToken enforces the strict string rules for record fields:
// nonempty, valid UTF-8, no NUL, and no surrounding or interior-only
// whitespace padding.
func validFieldToken(v string) error {
	if v == "" {
		return errors.New("empty")
	}
	if !utf8.ValidString(v) {
		return errors.New("invalid UTF-8")
	}
	if strings.ContainsRune(v, 0) {
		return errors.New("contains NUL")
	}
	if strings.TrimSpace(v) != v {
		return errors.New("surrounding whitespace")
	}
	return nil
}

// strictDecodeJSON decodes exactly one JSON value into v with no permissive
// fallback: unknown fields, duplicate object keys, and any trailing data are
// errors.
func strictDecodeJSON(data []byte, v any) error {
	if len(data) > 1024*1024 {
		return errors.New("policy record exceeds size limit")
	}
	if !utf8.Valid(data) {
		return errors.New("policy record contains invalid UTF-8")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty payload")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Exactly one JSON value: anything but EOF afterwards is trailing data.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("trailing data: %w", err)
		}
		return errors.New("trailing data")
	}
	return nil
}

// rejectDuplicateKeys walks the JSON token stream and refuses any object
// that repeats a key name (the standard decoder would silently take the last
// value, which is permissive decoding).
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyTok.(string)
				if !ok {
					return errors.New("malformed object key")
				}
				// All record field names are canonical lowercase snake_case.
				// encoding/json otherwise accepts case-insensitive aliases.
				if key == "" {
					return errors.New("empty policy field name")
				}
				for _, c := range key {
					if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
						return errors.New("noncanonical policy field name")
					}
				}
				if _, dup := seen[key]; dup {
					return fmt.Errorf("duplicate object key %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return err
			}
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return err
			}
		}
		return nil
	}
	return walk()
}
