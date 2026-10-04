// Package enterprise owns the local host's enterprise connection. It does not
// replace the inference routing/enforcement layer.
package enterprise

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

var (
	ErrDenied          = errors.New("enterprise authorization denied; sign in again")
	ErrUnavailable     = errors.New("enterprise service unavailable")
	ErrInvalidBundle   = errors.New("enterprise policy or skill verification failed")
	ErrCredentialStore = errors.New("enterprise credential store unavailable")
	ErrNotConnected    = errors.New("enterprise connection is not configured")
	ErrBusy            = errors.New("finish active managed work before changing enterprise connection")
)

const credentialKey = "active-connection"

type CredentialStore interface {
	Get(string) (string, error)
	Set(string, string) error
	Delete(string) error
}
type Credentials struct {
	Server          string    `json:"server"`
	OrganizationID  string    `json:"organization_id"`
	MemberID        string    `json:"member_id"`
	HostID          string    `json:"host_id"`
	Access          string    `json:"access_token"`
	Refresh         string    `json:"refresh_token"`
	ExpiresAt       time.Time `json:"expires_at"`
	HighestRevision int64     `json:"highest_revision"`
}
type Bundle struct {
	Membership *v1.Membership            `json:"membership,omitempty"`
	Envelope   v1.SignedPolicy           `json:"envelope"`
	Policy     v1.Policy                 `json:"policy"`
	Skills     []v1.SkillContentResponse `json:"skills"`
}
type Status struct {
	MembershipKnown  bool      `json:"membership_known"`
	OrganizationName string    `json:"organization_name,omitempty"`
	TeamID           string    `json:"team_id,omitempty"`
	TeamName         string    `json:"team_name,omitempty"`
	Connected        bool      `json:"connected"`
	OrganizationID   string    `json:"organization_id,omitempty"`
	HostID           string    `json:"host_id,omitempty"`
	Revision         int64     `json:"revision,omitempty"`
	ValidUntil       time.Time `json:"valid_until,omitempty"`
	Usable           bool      `json:"usable"`
	Error            string    `json:"error,omitempty"`
}
type Options struct {
	HTTPClient        *http.Client
	CachePath         string
	ClientVersion     string
	AllowLoopbackHTTP bool
}
type Manager struct {
	mu                  sync.Mutex
	store               CredentialStore
	client              *http.Client
	options             Options
	credentials         *Credentials
	bundle              *Bundle
	deadline, lastClock time.Time
	blocked             bool
	active              int
	lastError           string
}

func New(store CredentialStore, options Options) (*Manager, error) {
	if store == nil {
		return nil, ErrCredentialStore
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	copyClient := *client
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 15 * time.Second
	}
	m := &Manager{store: store, client: &copyClient, options: options, blocked: true}
	raw, err := store.Get(credentialKey)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, ErrCredentialStore
	}
	var c Credentials
	if json.Unmarshal([]byte(raw), &c) != nil || m.validOrigin(c.Server) != nil || !validUUID(c.OrganizationID) || !validUUID(c.MemberID) || !validUUID(c.HostID) {
		return nil, ErrCredentialStore
	}
	m.credentials = &c
	// Restart requires online revalidation. Disk cache is never a source of trust
	// or permission, and a changed system clock cannot extend a cached lease.
	return m, nil
}
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (m *Manager) validOrigin(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != raw {
		return errors.New("enterprise server must be a canonical HTTPS origin")
	}
	if u.Scheme == "https" {
		return nil
	}
	if m.options.AllowLoopbackHTTP && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1") {
		return nil
	}
	return errors.New("enterprise server requires HTTPS")
}
func (m *Manager) request(ctx context.Context, server, method, path, token string, in, out any, limit int64) error {
	var body io.Reader
	if in != nil {
		raw, e := json.Marshal(in)
		if e != nil {
			return ErrInvalidBundle
		}
		body = bytes.NewReader(raw)
	}
	r, e := http.NewRequestWithContext(ctx, method, server+path, body)
	if e != nil {
		return ErrUnavailable
	}
	if in != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := m.client.Do(r)
	if e != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return ErrDenied
	}
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		return ErrUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrInvalidBundle
	}
	if out == nil {
		return nil
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return ErrUnavailable
	}
	if int64(len(raw)) > limit || json.Unmarshal(raw, out) != nil {
		return ErrInvalidBundle
	}
	return nil
}
func (m *Manager) persist(c *Credentials) error {
	raw, e := json.Marshal(c)
	if e != nil || m.store.Set(credentialKey, string(raw)) != nil {
		return ErrCredentialStore
	}
	m.credentials = c
	return nil
}
func (m *Manager) refresh(ctx context.Context) error {
	c := *m.credentials
	if time.Now().Add(time.Minute).Before(c.ExpiresAt) {
		return nil
	}
	var grant tokenResponse
	if e := m.request(ctx, c.Server, "POST", "/auth/native/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": c.Refresh}, &grant, 8192); e != nil {
		return e
	}
	if grant.OrganizationID != c.OrganizationID || grant.MemberID != c.MemberID || grant.HostID != c.HostID || !grant.valid() {
		return ErrDenied
	}
	c.Access, c.Refresh, c.ExpiresAt = grant.Access, grant.Refresh, time.Now().Add(time.Duration(grant.ExpiresIn)*time.Second)
	if e := m.persist(&c); e != nil {
		_ = m.request(ctx, c.Server, "POST", "/auth/native/logout", c.Refresh, nil, nil, 0)
		return e
	}
	return nil
}
func (m *Manager) Sync(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.credentials == nil {
		return ErrNotConnected
	}
	now := time.Now()
	if !m.lastClock.IsZero() && now.Before(m.lastClock) {
		m.blocked = true
		m.lastError = "clock_changed"
		m.reportSyncFailure(ctx)
		return ErrInvalidBundle
	}
	m.lastClock = now
	err := m.sync(ctx)
	if err != nil {
		m.lastError = "unavailable"
		if !errors.Is(err, ErrUnavailable) {
			m.blocked = true
			m.lastError = "verification_failed"
		}
		if errors.Is(err, ErrDenied) {
			m.lastError = "authorization_denied"
		}
		if errors.Is(err, ErrCredentialStore) {
			m.lastError = "credential_store_unavailable"
		}
		m.reportSyncFailure(ctx)
		return err
	}
	m.lastError = ""
	return nil
}

// Reporting is optional and best effort. It cannot change enforcement or turn
// a failed synchronization into an acknowledgement. Bound the additional wait,
// respect cancellation, and send no raw error text or local information.
func (m *Manager) reportSyncFailure(ctx context.Context) {
	if ctx.Err() != nil || m.credentials == nil || m.credentials.HostID == "" || !v1.ValidSyncErrorCode(m.lastError) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c := m.credentials
	_ = m.request(ctx, c.Server, "PUT", "/v1/organizations/"+c.OrganizationID+"/hosts/"+c.HostID+"/sync-error", c.Access,
		v1.SyncFailure{Code: m.lastError, ClientVersion: m.options.ClientVersion}, nil, 0)
}

func (m *Manager) sync(ctx context.Context) error {
	if e := m.refresh(ctx); e != nil {
		return e
	}
	c := m.credentials
	var keys struct {
		Keys []struct{ Kty, Crv, Use, Alg, Kid, X string }
	}
	if e := m.request(ctx, c.Server, "GET", "/.well-known/cercano-policy-keys", "", nil, &keys, 16384); e != nil {
		return e
	}
	public := map[string]ed25519.PublicKey{}
	for _, key := range keys.Keys {
		raw, e := base64.RawURLEncoding.DecodeString(key.X)
		if e != nil || len(raw) != ed25519.PublicKeySize || key.Kty != "OKP" || key.Crv != "Ed25519" || key.Use != "sig" || key.Alg != "EdDSA" || key.Kid == "" || public[key.Kid] != nil {
			return ErrInvalidBundle
		}
		public[key.Kid] = raw
	}
	var response v1.EffectivePolicyResponse
	base := "/v1/organizations/" + c.OrganizationID
	if e := m.request(ctx, c.Server, "GET", base+"/hosts/"+c.HostID+"/policy", c.Access, nil, &response, 2*v1.MaxPolicyBytes); e != nil {
		return e
	}
	if response.SchemaVersion != v1.Version {
		return ErrInvalidBundle
	}
	envelope := response.Policy
	payload, e := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if e != nil || len(payload) > v1.MaxPolicyBytes {
		return ErrInvalidBundle
	}
	signature, e := base64.RawURLEncoding.DecodeString(envelope.Signature)
	key := public[envelope.KeyID]
	if e != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, append([]byte("cercano-enterprise-policy-v1\n"), payload...), signature) {
		return ErrInvalidBundle
	}
	p, e := v1.DecodePolicy(payload)
	now := time.Now()
	scope := v1.Scope{OrganizationID: c.OrganizationID, UserID: c.MemberID, HostID: c.HostID}
	if e != nil || p.Validate(scope, now) != nil || p.Revision < c.HighestRevision || !versionAtLeast(m.options.ClientVersion, p.MinimumClientVersion) {
		return ErrInvalidBundle
	}
	// Membership is display-only HTTPS metadata, never an authorization input.
	if membership := response.Membership; membership != nil {
		if membership.OrganizationID != scope.OrganizationID || membership.UserID != scope.UserID ||
			(membership.Role != "administrator" && membership.Role != "developer") ||
			(membership.TeamID != "" && !validUUID(membership.TeamID)) ||
			(membership.TeamID == "" && membership.TeamName != "") {
			return ErrInvalidBundle
		}
	}
	b := Bundle{Envelope: envelope, Policy: p, Membership: response.Membership, Skills: []v1.SkillContentResponse{}}
	for _, assignment := range p.Skills {
		var skill v1.SkillContentResponse
		found := false
		if m.bundle != nil {
			for _, cached := range m.bundle.Skills {
				if cached.ID == assignment.ID && cached.Version == assignment.Version && skillMatches(cached, assignment) {
					skill = cached
					found = true
					break
				}
			}
		}
		if !found {
			if e = m.request(ctx, c.Server, "GET", base+"/skills/"+url.PathEscape(assignment.ID)+"/versions/"+url.PathEscape(assignment.Version), c.Access, nil, &skill, 6*v1.MaxSkillBytes+4096); e != nil {
				return e
			}
		}
		if !skillMatches(skill, assignment) {
			return ErrInvalidBundle
		}
		b.Skills = append(b.Skills, skill)
	}
	// Verify expiry again after downloads before atomically activating the bundle.
	now = time.Now()
	if p.Validate(scope, now) != nil {
		return ErrInvalidBundle
	}
	if e = m.saveBundle(b); e != nil {
		return e
	}
	updated := *c
	updated.HighestRevision = p.Revision
	if e = m.persist(&updated); e != nil {
		return e
	}
	m.bundle = &b
	m.deadline = now.Add(p.ExpiresAt.Sub(now))
	m.blocked = false
	return m.request(ctx, c.Server, "PUT", base+"/hosts/"+c.HostID+"/applied", c.Access, v1.SyncAcknowledgement{PolicyRevision: p.Revision, ClientVersion: m.options.ClientVersion, Skills: p.Skills}, nil, 0)
}
func skillMatches(s v1.SkillContentResponse, a v1.SkillAssignment) bool {
	return s.SchemaVersion == v1.Version && s.ID == a.ID && s.Version == a.Version && len(s.Content) == a.SizeBytes && fmt.Sprintf("%x", sha256.Sum256([]byte(s.Content))) == a.SHA256
}
func versionAtLeast(actual, minimum string) bool {
	parse := func(raw string) ([3]uint64, bool) {
		var n [3]uint64
		parts := strings.Split(raw, ".")
		if len(parts) != 3 {
			return n, false
		}
		for i, p := range parts {
			v, e := strconv.ParseUint(p, 10, 64)
			if e != nil || strconv.FormatUint(v, 10) != p {
				return n, false
			}
			n[i] = v
		}
		return n, true
	}
	a, ok := parse(actual)
	b, valid := parse(minimum)
	if !ok || !valid {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}
func (m *Manager) saveBundle(b Bundle) error {
	if m.options.CachePath == "" {
		return nil
	}
	dir := filepath.Dir(m.options.CachePath)
	if os.MkdirAll(dir, 0700) != nil {
		return ErrInvalidBundle
	}
	raw, e := json.Marshal(b)
	if e != nil {
		return ErrInvalidBundle
	}
	f, e := os.CreateTemp(dir, ".enterprise-bundle-")
	if e != nil {
		return ErrInvalidBundle
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil || os.Rename(name, m.options.CachePath) != nil {
		return ErrInvalidBundle
	}
	return nil
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{Error: m.lastError}
	if m.credentials == nil {
		return s
	}
	s.Connected = true
	s.OrganizationID = m.credentials.OrganizationID
	s.HostID = m.credentials.HostID
	if m.bundle != nil {
		s.Revision = m.bundle.Policy.Revision
		s.ValidUntil = m.bundle.Policy.ExpiresAt
		if membership := m.bundle.Membership; membership != nil {
			s.MembershipKnown = true
			s.OrganizationName = membership.OrganizationName
			s.TeamID = membership.TeamID
			s.TeamName = membership.TeamName
		}
	}
	s.Usable = m.usable()
	return s
}
func (m *Manager) usable() bool {
	return m.bundle != nil && !m.blocked && time.Now().Before(m.deadline) && !time.Now().Before(m.lastClock)
}

// Begin pins the verified bundle for a turn. Enforcement must still consult
// Current before each physical inference attempt to apply newer restrictions.
func (m *Manager) Begin() (Bundle, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.usable() {
		return Bundle{}, nil, ErrDenied
	}
	m.active++
	var once sync.Once
	raw, _ := json.Marshal(m.bundle)
	var b Bundle
	_ = json.Unmarshal(raw, &b)
	return b, func() { once.Do(func() { m.mu.Lock(); m.active--; m.mu.Unlock() }) }, nil
}
func (m *Manager) Current() (v1.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.usable() {
		return v1.Policy{}, ErrDenied
	}
	raw, _ := json.Marshal(m.bundle.Policy)
	var p v1.Policy
	_ = json.Unmarshal(raw, &p)
	return p, nil
}
func (m *Manager) Logout(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active > 0 {
		return ErrBusy
	}
	m.blocked = true
	m.bundle = nil
	if m.credentials == nil {
		return nil
	}
	c := m.credentials
	remoteErr := m.request(ctx, c.Server, "POST", "/auth/native/logout", c.Refresh, nil, nil, 0)
	if m.store.Delete(credentialKey) != nil {
		return ErrCredentialStore
	}
	m.credentials = nil
	if m.options.CachePath != "" {
		if e := os.Remove(m.options.CachePath); e != nil && !errors.Is(e, os.ErrNotExist) {
			return ErrInvalidBundle
		}
	}
	return remoteErr
}

// Run refreshes about once a minute and backs off transient failures. Shutdown
// cancels requests; an outage never extends an existing policy deadline.
func (m *Manager) Run(ctx context.Context) {
	delay := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		e := m.Sync(ctx)
		if errors.Is(e, ErrDenied) || errors.Is(e, ErrNotConnected) {
			return
		}
		if e != nil {
			if delay < time.Minute {
				delay = time.Minute
			} else {
				delay *= 2
			}
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}
		} else {
			var random [1]byte
			_, _ = io.ReadFull(rand.Reader, random[:])
			delay = time.Duration(55+int(random[0])%11) * time.Second
		}
	}
}
