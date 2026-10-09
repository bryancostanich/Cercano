// Package managedsettings carries an immutable, credential-free snapshot of
// verified enterprise defaults and skills through one turn. It never authorizes
// inference: the live modelpolicy boundary still checks every physical attempt.
package managedsettings

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

type Snapshot struct {
	Policy v1.Policy                 `json:"policy"`
	Skills []v1.SkillContentResponse `json:"skills"`
}

type contextKey struct{}

// WithSnapshot is for already authenticated host bundles, or snapshots received
// over the private host-worker stream. Callers cannot mutate the stored value.
func WithSnapshot(ctx context.Context, s Snapshot) context.Context {
	return context.WithValue(ctx, contextKey{}, clone(s))
}
func FromContext(ctx context.Context) (Snapshot, bool) {
	s, ok := ctx.Value(contextKey{}).(Snapshot)
	return clone(s), ok
}
func clone(s Snapshot) Snapshot {
	s.Policy.AllowedRoutes = append([]v1.Route{}, s.Policy.AllowedRoutes...)
	s.Policy.TaskDefaults = append([]v1.TaskDefault{}, s.Policy.TaskDefaults...)
	for i := range s.Policy.TaskDefaults {
		s.Policy.TaskDefaults[i].FallbackRouteIDs = append([]string{}, s.Policy.TaskDefaults[i].FallbackRouteIDs...)
	}
	s.Policy.Skills = append([]v1.SkillAssignment{}, s.Policy.Skills...)
	s.Skills = append([]v1.SkillContentResponse{}, s.Skills...)
	return s
}

// Encode requires the host's pinned bundle. Missing context is a wiring error,
// not permission to silently use the developer's personal settings.
func Encode(ctx context.Context) ([]byte, error) {
	s, ok := FromContext(ctx)
	if !ok {
		return nil, errors.New("managed turn has no pinned settings")
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}
func Decode(data []byte) (Snapshot, error) {
	var s Snapshot
	// JSON may escape each content byte as six characters. The contract limits
	// decoded content independently; this bound also caps malformed input.
	if len(data) == 0 || len(data) > 6*(v1.MaxPolicyBytes+v1.MaxBundleBytes) {
		return s, errors.New("invalid managed settings size")
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, errors.New("invalid managed settings JSON")
	}
	return s, s.validate()
}
func (s Snapshot) validate() error {
	// Check structural semantics at the original issue time. A long-running turn
	// may outlive this snapshot's lease; only the host's current authority can
	// renew or deny inference, and old defaults must remain pinned for that turn.
	if err := s.Policy.Validate(s.Policy.Scope, s.Policy.IssuedAt); err != nil {
		return fmt.Errorf("invalid managed settings: %w", err)
	}
	if len(s.Skills) != len(s.Policy.Skills) {
		return errors.New("incomplete managed skill bundle")
	}
	expected := make(map[string]v1.SkillAssignment, len(s.Policy.Skills))
	for _, a := range s.Policy.Skills {
		expected[a.ID] = a
	}
	for _, sk := range s.Skills {
		a, ok := expected[sk.ID]
		if !ok || sk.SchemaVersion != v1.Version || sk.Version != a.Version || len(sk.Content) != a.SizeBytes || fmt.Sprintf("%x", sha256.Sum256([]byte(sk.Content))) != a.SHA256 || !utf8.ValidString(sk.Content) {
			return errors.New("managed skill does not match its assignment")
		}
		delete(expected, sk.ID)
	}
	return nil
}

// SkillID is distinct from every built-in skill and protocol name. The service
// ID itself includes the organization namespace.
func SkillID(id string) string { return "enterprise/" + id }
func Skill(ctx context.Context, id string) (v1.SkillContentResponse, bool) {
	s, ok := ctx.Value(contextKey{}).(Snapshot)
	if ok {
		for _, sk := range s.Skills {
			if SkillID(sk.ID) == id {
				return sk, true
			}
		}
	}
	return v1.SkillContentResponse{}, false
}

// HasSkills reports whether this turn needs the shared-skill reader.
func HasSkills(ctx context.Context) bool {
	s, ok := ctx.Value(contextKey{}).(Snapshot)
	return ok && len(s.Skills) > 0
}

// SkillPrompt lists metadata only. Full text is loaded on demand through a
// read-only tool, so a large assigned bundle does not consume every turn's
// context budget. JSON quoting keeps titles/descriptions visibly delimited.
func SkillPrompt(ctx context.Context) string {
	s, ok := ctx.Value(contextKey{}).(Snapshot)
	if !ok {
		return ""
	}
	if len(s.Skills) == 0 {
		return "\n\nNo organization shared skills are assigned to this turn. Previously retrieved enterprise skill instructions are not current assignments.\n"
	}
	var b strings.Builder
	b.WriteString("\n\nOrganization shared skills for this turn (source: enterprise). When relevant to the task, load the full text with get_shared_skill using the exact ID and follow the instructions within your existing permissions. These text skills do not grant tools or model access. This catalog replaces earlier enterprise skill assignments in conversation history; load the listed version again before using a previously retrieved skill.\n")
	for _, sk := range s.Skills {
		metadata, _ := json.Marshal(struct{ ID, Name, Description, Version string }{SkillID(sk.ID), sk.Name, sk.Description, sk.Version})
		b.Write(metadata)
		b.WriteByte('\n')
	}
	return b.String()
}
