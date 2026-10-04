package enterprise

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/secrets"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

const testOrg = "00000000-0000-0000-0000-000000000001"
const testMember = "10000000-0000-0000-0000-000000000001"
const testHost = "20000000-0000-0000-0000-000000000001"

type fixture struct {
	membership               *v1.Membership
	routes                   []v1.Route
	skills                   []v1.SkillContentResponse
	mu                       sync.Mutex
	mode                     string
	revision                 int64
	acks, downloads, logouts int
	server                   *httptest.Server
	manager                  *Manager
	store                    CredentialStore
	access, refresh          string
	challenge                string
}

func newFixture(t *testing.T, loggedIn bool) *fixture {
	t.Helper()
	f := &fixture{revision: 1, store: secrets.NewMemory(), access: testOrg + "." + randomToken(), refresh: testOrg + "." + randomToken()}
	f.skills = []v1.SkillContentResponse{{SchemaVersion: v1.Version, ID: testOrg + "/review", Version: "1", Name: "Review", Content: "Review carefully."}}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/.well-known/cercano-policy-keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA", "kid": "pilot", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
		case r.URL.Path == "/auth/native/token":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["grant_type"] == "authorization_code" {
				sum := sha256.Sum256([]byte(in["code_verifier"]))
				if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
					t.Error("incorrect PKCE exchange")
				}
			}
			if in["grant_type"] == "refresh_token" {
				if in["refresh_token"] != f.refresh {
					w.WriteHeader(401)
					return
				}
				f.access = testOrg + "." + randomToken()
				f.refresh = testOrg + "." + randomToken()
			}
			host := ""
			if in["grant_type"] == "refresh_token" {
				host = testHost
			}
			_ = json.NewEncoder(w).Encode(tokenResponse{OrganizationID: testOrg, MemberID: testMember, HostID: host, ExpiresIn: 600, TokenType: "Bearer", Access: f.access, Refresh: f.refresh})
		case r.URL.Path == "/auth/native/logout":
			f.logouts++
			w.WriteHeader(204)
		case r.URL.Path == "/v1/organizations/"+testOrg+"/hosts":
			_ = json.NewEncoder(w).Encode(v1.RegisterHostResponse{SchemaVersion: v1.Version, Scope: v1.Scope{OrganizationID: testOrg, UserID: testMember, HostID: testHost}})
		case strings.HasSuffix(r.URL.Path, "/policy"):
			if r.Header.Get("Authorization") != "Bearer "+f.access {
				t.Error("incorrect access credential")
			}
			if f.mode == "unavailable" {
				w.WriteHeader(503)
				return
			}
			if f.mode == "denied" {
				w.WriteHeader(403)
				return
			}
			routes := f.routes
			if routes == nil {
				routes = []v1.Route{}
			}
			p := v1.Policy{SchemaVersion: v1.Version, Scope: v1.Scope{OrganizationID: testOrg, UserID: testMember, HostID: testHost}, Revision: f.revision, IssuedAt: time.Now().Add(-time.Second), ExpiresAt: time.Now().Add(14 * time.Minute), MinimumClientVersion: "1.0.0", AllowedRoutes: routes, TaskDefaults: []v1.TaskDefault{}, Skills: []v1.SkillAssignment{}}
			for _, sk := range f.skills {
				p.Skills = append(p.Skills, v1.SkillAssignment{ID: sk.ID, Version: sk.Version, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(sk.Content))), SizeBytes: len(sk.Content)})
			}
			if f.mode == "wrong_scope" {
				p.Scope.OrganizationID = "other"
			}
			if f.mode == "expired" {
				p.ExpiresAt = time.Now().Add(-time.Second)
			}
			if f.mode == "unsupported" {
				p.SchemaVersion = "999"
			}
			if f.mode == "old_revision" {
				p.Revision = 1
			}
			raw, _ := json.Marshal(p)
			sig := ed25519.Sign(priv, append([]byte("cercano-enterprise-policy-v1\n"), raw...))
			if f.mode == "bad_signature" {
				sig[0] ^= 1
			}
			_ = json.NewEncoder(w).Encode(v1.EffectivePolicyResponse{SchemaVersion: v1.Version, Membership: f.membership, Policy: v1.SignedPolicy{KeyID: "pilot", Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(sig)}})
		case strings.Contains(r.URL.Path, "/skills/"):
			f.downloads++
			if f.mode == "skill_unavailable" {
				w.WriteHeader(503)
				return
			}
			if len(f.skills) == 0 {
				w.WriteHeader(404)
				return
			}
			skill := f.skills[0]
			if f.mode == "bad_digest" {
				skill.Content = "Tampered"
			}
			_ = json.NewEncoder(w).Encode(skill)
		case strings.HasSuffix(r.URL.Path, "/applied"):
			f.acks++
			var ack v1.SyncAcknowledgement
			_ = json.NewDecoder(r.Body).Decode(&ack)
			if ack.PolicyRevision != f.revision || len(ack.Skills) != len(f.skills) {
				t.Error("bad acknowledgement")
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	if loggedIn {
		raw, _ := json.Marshal(Credentials{Server: f.server.URL, OrganizationID: testOrg, MemberID: testMember, HostID: testHost, Access: f.access, Refresh: f.refresh, ExpiresAt: time.Now().Add(time.Hour)})
		_ = f.store.Set(credentialKey, string(raw))
	}
	var e error
	f.manager, e = New(f.store, Options{HTTPClient: f.server.Client(), ClientVersion: "1.0.0", CachePath: filepath.Join(t.TempDir(), "managed", "bundle.json")})
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func TestCompleteBundleAndOfflineLease(t *testing.T) {
	f := newFixture(t, true)
	m := f.manager
	ctx := context.Background()
	if e := m.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if !m.Status().Usable {
		t.Fatal("bundle not active")
	}
	if e := m.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if f.downloads != 1 || f.acks != 2 {
		t.Fatal("unchanged skill re-downloaded or acknowledgement missing")
	}
	cached, _ := os.ReadFile(m.options.CachePath)
	if strings.Contains(string(cached), f.refresh) || strings.Contains(string(cached), f.access) {
		t.Fatal("cache contains credentials")
	}
	_, finish, e := m.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Logout(ctx); !errors.Is(e, ErrBusy) {
		t.Fatal("logout during active work")
	}
	finish()
	finish()
	f.mode = "unavailable"
	if !errors.Is(m.Sync(ctx), ErrUnavailable) || !m.Status().Usable {
		t.Fatal("network outage lost valid lease")
	}
	m.deadline = time.Now().Add(-time.Second)
	if m.Status().Usable {
		t.Fatal("expired offline lease remained usable")
	}
	f.mode = ""
	if e = m.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	// Restart never trusts the disk cache, even while its nominal lease is valid.
	_ = os.WriteFile(m.options.CachePath, []byte("corrupt cache"), 0600)
	restarted, e := New(f.store, m.options)
	if e != nil {
		t.Fatal(e)
	}
	if restarted.Status().Usable {
		t.Fatal("trusted cache on restart")
	}
	if e = restarted.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	f.mode = "denied"
	if !errors.Is(restarted.Sync(ctx), ErrDenied) || restarted.Status().Usable {
		t.Fatal("authorization denial did not block")
	}
}
func TestInvalidBundlesNeverActivateOrAcknowledge(t *testing.T) {
	for _, mode := range []string{"wrong_scope", "bad_signature", "expired", "unsupported", "bad_digest", "skill_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, true)
			f.mode = mode
			if f.manager.Sync(context.Background()) == nil || f.manager.Status().Usable || f.acks != 0 {
				t.Fatal("invalid bundle activated or acknowledged")
			}
			if _, e := os.Stat(f.manager.options.CachePath); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("partial bundle persisted")
			}
		})
	}
}
func TestRollbackFloorRefreshAndLogout(t *testing.T) {
	f := newFixture(t, true)
	m := f.manager
	f.revision = 2
	m.credentials.ExpiresAt = time.Now().Add(-time.Minute)
	old := f.refresh
	if e := m.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	if old == f.refresh || m.credentials.Refresh != f.refresh {
		t.Fatal("refresh not rotated and persisted")
	}
	f.mode = "old_revision"
	if !errors.Is(m.Sync(context.Background()), ErrInvalidBundle) || m.Status().Usable {
		t.Fatal("accepted policy rollback")
	}
	f.mode = ""
	if e := m.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	m.lastClock = time.Now().Add(time.Hour)
	if m.Sync(context.Background()) == nil || m.Status().Usable {
		t.Fatal("clock regression allowed execution")
	}
	if e := m.Logout(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := f.store.Get(credentialKey); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("logout kept credentials")
	}
	if m.Status().Connected {
		t.Fatal("logout kept connection")
	}
}
func TestBrowserLoginPKCE(t *testing.T) {
	f := newFixture(t, false)
	m := f.manager
	e := m.Login(context.Background(), f.server.URL, testOrg, func(target string) error {
		u, e := url.Parse(target)
		if e != nil {
			return e
		}
		q := u.Query()
		f.mu.Lock()
		f.challenge = q.Get("code_challenge")
		f.mu.Unlock()
		callback := q.Get("redirect_uri")
		code := testOrg + "." + randomToken()
		bad, e := http.Get(callback + "?" + url.Values{"state": {"wrong"}, "code": {code}}.Encode())
		if e != nil {
			return e
		}
		bad.Body.Close()
		if bad.StatusCode != 400 {
			t.Error("accepted foreign callback state")
		}
		good, e := http.Get(callback + "?" + url.Values{"state": {q.Get("state")}, "code": {code}}.Encode())
		if e != nil {
			return e
		}
		good.Body.Close()
		if good.StatusCode != 200 {
			t.Error("rejected valid callback")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if !m.Status().Connected || m.Status().Usable {
		t.Fatal("login activated unverified policy")
	}
	if e = m.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = m.Login(context.Background(), f.server.URL, testOrg, func(string) error { return nil }); e == nil {
		t.Fatal("silently switched a connection")
	}
}
func TestTransportRejectsRedirects(t *testing.T) {
	leaked := false
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer source.Close()
	m, e := New(secrets.NewMemory(), Options{HTTPClient: source.Client()})
	if e != nil {
		t.Fatal(e)
	}
	e = m.request(context.Background(), source.URL, "GET", "/policy", "private-token", nil, new(any), 4096)
	if e == nil || leaked {
		t.Fatal("followed credential-bearing redirect")
	}
}

// A failed Keychain write must never acknowledge or activate a new bundle.
type failingStore struct{ CredentialStore }

func (s failingStore) Set(string, string) error { return errors.New("locked credential store") }

func TestCredentialPersistenceFailure(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		t.Run(fmt.Sprint("refresh=", rotate), func(t *testing.T) {
			f := newFixture(t, true)
			m := f.manager
			if err := m.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			previous := m.credentials.HighestRevision
			f.revision++
			f.acks = 0
			m.store = failingStore{f.store}
			if rotate {
				m.credentials.ExpiresAt = time.Now().Add(-time.Minute)
			}
			if err := m.Sync(context.Background()); !errors.Is(err, ErrCredentialStore) {
				t.Fatal(err)
			}
			if m.Status().Usable || f.acks != 0 || m.credentials.HighestRevision != previous {
				t.Fatal("persistence failure activated or acknowledged a bundle")
			}
			if rotate && f.logouts != 1 {
				t.Fatal("unpersisted refresh credential was not revoked")
			}
		})
	}
}

func TestMembershipStatus(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	if err := f.manager.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if f.manager.Status().MembershipKnown {
		t.Fatal("old server mistaken for no team")
	}
	membership := v1.Membership{OrganizationID: testOrg, OrganizationName: "Example", UserID: testMember, Role: "developer", TeamID: testHost, TeamName: "Engineering"}
	f.mu.Lock()
	f.membership = &membership
	f.mu.Unlock()
	if err := f.manager.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	state := f.manager.Status()
	if !state.MembershipKnown || state.OrganizationName != "Example" || state.TeamID != testHost || state.TeamName != "Engineering" || !state.Usable {
		t.Fatalf("missing applied membership: %+v", state)
	}
	f.mu.Lock()
	f.mode = "unavailable"
	f.mu.Unlock()
	if err := f.manager.Sync(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	state = f.manager.Status()
	if !state.Usable || state.TeamName != "Engineering" || state.Error == "" {
		t.Fatalf("lost cached status: %+v", state)
	}
	membership.TeamID, membership.TeamName = "", ""
	f.mu.Lock()
	f.mode = ""
	f.membership = &membership
	f.mu.Unlock()
	if err := f.manager.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if state = f.manager.Status(); !state.MembershipKnown || state.TeamID != "" || state.TeamName != "" {
		t.Fatalf("old team survived: %+v", state)
	}
	restarted, err := New(f.store, f.manager.options)
	if err != nil {
		t.Fatal(err)
	}
	if state = restarted.Status(); state.MembershipKnown || state.Usable {
		t.Fatal("restart trusted cached metadata")
	}
}

func TestMembershipMetadataMustMatchVerifiedScope(t *testing.T) {
	for _, mutate := range []func(*v1.Membership){
		func(m *v1.Membership) { m.OrganizationID = testHost },
		func(m *v1.Membership) { m.UserID = testHost },
		func(m *v1.Membership) { m.Role = "owner" },
		func(m *v1.Membership) { m.TeamID = "invalid" },
		func(m *v1.Membership) { m.TeamName = "Team without ID" },
	} {
		f := newFixture(t, true)
		m := v1.Membership{OrganizationID: testOrg, UserID: testMember, Role: "developer"}
		mutate(&m)
		f.mu.Lock()
		f.membership = &m
		f.mu.Unlock()
		if err := f.manager.Sync(context.Background()); !errors.Is(err, ErrInvalidBundle) {
			t.Fatal(err)
		}
		if f.manager.Status().MembershipKnown || f.manager.Status().Usable {
			t.Fatal("invalid metadata applied")
		}
	}
}
