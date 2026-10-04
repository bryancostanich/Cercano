package v1

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,127}$`)
var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// DecodePolicy strictly decodes the unsigned payload. Call it only after
// authenticating its signed envelope; this function does not verify signatures.
func DecodePolicy(data []byte) (Policy, error) {
	var p Policy
	if len(data) > MaxPolicyBytes {
		return p, errors.New("policy exceeds size limit")
	}
	if !utf8.Valid(data) {
		return p, errors.New("policy must be UTF-8")
	}
	// Reject duplicate keys too: different decoders must not disagree on which
	// organization or permissions a signed document contains.
	d := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueKeys(d); err != nil {
		return p, err
	}
	if _, err := d.Token(); err != io.EOF {
		return p, errors.New("trailing JSON data")
	}
	if err := validateShape(data); err != nil {
		return p, err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	return p, nil
}

func uniqueKeys(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := k.(string)
			if !ok {
				return errors.New("invalid object key")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = true
			if err := uniqueKeys(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueKeys(d); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}

// Validate checks payload semantics against an independently authenticated
// scope and a trusted time. It does not validate client version compatibility,
// revision monotonicity, signature trust, or actual request destinations.
func (p Policy) Validate(expected Scope, now time.Time) error {
	if p.SchemaVersion != Version {
		return errors.New("unsupported schema version")
	}
	if !identifier.MatchString(p.Scope.OrganizationID) || !identifier.MatchString(p.Scope.UserID) || !identifier.MatchString(p.Scope.HostID) || p.Scope != expected {
		return errors.New("policy scope mismatch")
	}
	if p.Revision < 1 {
		return errors.New("revision must be positive")
	}
	if p.IssuedAt.IsZero() || p.IssuedAt.After(now) || !p.ExpiresAt.After(now) || !p.ExpiresAt.After(p.IssuedAt) || p.ExpiresAt.Sub(p.IssuedAt) > MaxLease {
		return errors.New("invalid or expired policy lease")
	}
	if !releaseVersion.MatchString(p.MinimumClientVersion) {
		return errors.New("minimum client version must be major.minor.patch")
	}
	if p.AllowedRoutes == nil || p.TaskDefaults == nil || p.Skills == nil {
		return errors.New("policy lists must be explicit arrays")
	}
	routes := map[string]Route{}
	for _, r := range p.AllowedRoutes {
		if !identifier.MatchString(r.ID) || !identifier.MatchString(r.Provider) || strings.TrimSpace(r.Model) == "" || len(r.Model) > 256 {
			return errors.New("invalid route identity")
		}
		if _, exists := routes[r.ID]; exists {
			return errors.New("duplicate route ID")
		}
		if err := validateEndpoint(r); err != nil {
			return err
		}
		routes[r.ID] = r
	}
	tasks := map[string]bool{}
	for _, t := range p.TaskDefaults {
		if !validTask(t.Task) || tasks[t.Task] {
			return errors.New("unknown or duplicate task")
		}
		tasks[t.Task] = true
		if t.Destination != "primary" && t.Destination != "secondary" && t.Destination != "local" {
			return errors.New("invalid destination")
		}
		if t.Quality != "economy" && t.Quality != "standard" && t.Quality != "premium" {
			return errors.New("invalid quality")
		}
		if t.FallbackRouteIDs == nil {
			return errors.New("fallback routes must be an explicit array")
		}
		seen := map[string]bool{}
		for _, id := range append([]string{t.RouteID}, t.FallbackRouteIDs...) {
			r, ok := routes[id]
			if !ok || seen[id] {
				return errors.New("unknown or repeated route in task assignment")
			}
			seen[id] = true
			if t.Destination == "local" && r.Placement != "local" {
				return errors.New("local destination cannot use an external route")
			}
		}
	}
	skills := map[string]bool{}
	total := 0
	for _, s := range p.Skills {
		if !identifier.MatchString(s.ID) || !strings.Contains(s.ID, "/") || !identifier.MatchString(s.Version) || skills[s.ID] {
			return errors.New("invalid or conflicting skill assignment")
		}
		skills[s.ID] = true
		digest, err := hex.DecodeString(s.SHA256)
		if err != nil || len(digest) != 32 || s.SHA256 != strings.ToLower(s.SHA256) {
			return errors.New("invalid skill digest")
		}
		if s.SizeBytes < 1 || s.SizeBytes > MaxSkillBytes {
			return errors.New("invalid skill size")
		}
		total += s.SizeBytes
		if total > MaxBundleBytes {
			return errors.New("skill bundle exceeds size limit")
		}
	}
	return nil
}

func validTask(task string) bool {
	switch task {
	case "chat", "compaction", "dispatch", "reconnaissance", "mechanical_development", "investigation", "implementation", "review", "research", "git_land", "watchdog":
		return true
	}
	return false
}

func validateEndpoint(r Route) error {
	u, err := url.Parse(r.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Host != strings.ToLower(u.Host) || strings.HasSuffix(r.Endpoint, "/") || u.String() != r.Endpoint {
		return errors.New("endpoint must be a canonical base URL without credentials, query, fragment, or trailing slash")
	}
	if r.Placement == "local" {
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("local route must use a literal loopback address")
		}
	} else if r.Placement != "external" || u.Scheme != "https" {
		return errors.New("external route must use HTTPS")
	}
	return nil
}
