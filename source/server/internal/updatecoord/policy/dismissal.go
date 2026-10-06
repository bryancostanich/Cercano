package policy

import (
	"encoding/json"
	"fmt"
	"sync"
)

// DismissalSchemaVersion is the current dismissal record schema version.
// Records with any other schema version are rejected on decode.
const DismissalSchemaVersion = 1

// Dismissal is the user's explicit "not now" for ONE release of ONE
// installation. It is bound to the opaque installation identifier, the
// release channel, the update source, and the exact version. There is
// deliberately NO global "suppress next version" or "stop telling me"
// semantic: a new version announced for any channel, source, or installation
// is never suppressed by an existing dismissal.
type Dismissal struct {
	// InstallID is the opaque installation identifier from the trusted
	// resolver.
	InstallID string
	// Channel is the release channel the announcement was for (for example
	// "stable").
	Channel string
	// Source is the update source the announcement was verified for (for
	// example "tuf").
	Source string
	// Version is the exact release version being dismissed.
	Version string
}

// DismissalRecord is the persisted form of a Dismissal. Decode is
// security-critical and strict; see DecodeDismissalRecord.
type DismissalRecord struct {
	// SchemaVersion must equal DismissalSchemaVersion.
	SchemaVersion int `json:"schema_version"`
	// InstallID is the dismissed installation's opaque identifier.
	InstallID string `json:"install_id"`
	// Channel is the dismissed release channel.
	Channel string `json:"channel"`
	// Source is the dismissed update source.
	Source string `json:"source"`
	// Version is the dismissed exact release version.
	Version string `json:"version"`
}

// Encode serializes the dismissal after validation, forcing the current
// schema version. It writes nothing anywhere; the caller owns any
// persistence decision.
func (d Dismissal) Encode() ([]byte, error) {
	if err := d.validate(); err != nil {
		return nil, fmt.Errorf("dismissal: %w", err)
	}
	rec := DismissalRecord{
		SchemaVersion: DismissalSchemaVersion,
		InstallID:     d.InstallID,
		Channel:       d.Channel,
		Source:        d.Source,
		Version:       d.Version,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("dismissal: marshal: %w", err)
	}
	return data, nil
}

// DecodeDismissalRecord strictly decodes and validates a dismissal record.
// Unknown fields, unknown schema versions, duplicate keys, trailing data,
// malformed JSON, and invalid or empty field values are all rejected. There
// is no permissive mode.
func DecodeDismissalRecord(data []byte) (Dismissal, error) {
	var rec DismissalRecord
	if err := strictDecodeJSON(data, &rec); err != nil {
		return Dismissal{}, fmt.Errorf("dismissal: %w", err)
	}
	if rec.SchemaVersion != DismissalSchemaVersion {
		return Dismissal{}, fmt.Errorf("dismissal: unknown schema version %d, want %d", rec.SchemaVersion, DismissalSchemaVersion)
	}
	d := Dismissal{
		InstallID: rec.InstallID,
		Channel:   rec.Channel,
		Source:    rec.Source,
		Version:   rec.Version,
	}
	if err := d.validate(); err != nil {
		return Dismissal{}, fmt.Errorf("dismissal: %w", err)
	}
	return d, nil
}

func (d Dismissal) validate() error {
	for field, value := range map[string]string{
		"install_id": d.InstallID,
		"channel":    d.Channel,
		"source":     d.Source,
		"version":    d.Version,
	} {
		if err := validFieldToken(value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	return nil
}

// DismissalStore is an in-memory set of version-scoped dismissals. It is a
// value holder only: it performs no I/O, has no global singleton, and is
// safe for concurrent use. Persistence (if any) is the caller's decision;
// records must round-trip through Encode/DecodeDismissalRecord.
type DismissalStore struct {
	mu        sync.Mutex
	dismissed map[Dismissal]struct{}
}

// NewDismissalStore returns an empty store.
func NewDismissalStore() *DismissalStore {
	return &DismissalStore{dismissed: make(map[Dismissal]struct{})}
}

// Dismiss records a validated version-scoped dismissal. It never widens the
// dismissal's scope: storing {a, stable, tuf, 1.2.3} suppresses exactly that
// announcement.
func (s *DismissalStore) Dismiss(d Dismissal) error {
	if err := d.validate(); err != nil {
		return fmt.Errorf("dismissal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dismissed[d] = struct{}{}
	return nil
}

// IsDismissed reports whether the exact announcement (installation, channel,
// source, and version) was dismissed. Any difference — a newer version, a
// different channel or source, or a different installation — reports false:
// there is no global suppress-next-version semantic.
func (s *DismissalStore) IsDismissed(d Dismissal) bool {
	if d == (Dismissal{}) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.dismissed[d]
	return ok
}
