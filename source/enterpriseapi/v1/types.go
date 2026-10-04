// Package v1 defines the enterprise service wire contract. It has no dependency
// on Cercano's agent, provider integrations, or private implementation packages.
// These types describe a draft protocol; they do not authenticate requests or
// enforce policy in the existing Cercano host.
package v1

import "time"

const (
	Version        = "1"
	MaxPolicyBytes = 256 * 1024
	MaxSkillBytes  = 256 * 1024
	MaxBundleBytes = 2 * 1024 * 1024
	MaxLease       = 15 * time.Minute
)

// Scope binds a policy to one organization, user, and enrolled host. UserID is
// the enterprise membership ID, not an email address or an OIDC subject alone.
type Scope struct {
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
	HostID         string `json:"host_id"`
}

type Membership struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	UserID           string `json:"user_id"`
	Role             string `json:"role"`
	TeamID           string `json:"team_id,omitempty"`
}

type MembershipsResponse struct {
	SchemaVersion string       `json:"schema_version"`
	Memberships   []Membership `json:"memberships"`
}

// RegisterHostRequest deliberately excludes machine paths, environment
// contents, and provider credentials. The server assigns the host ID.
type RegisterHostRequest struct {
	DisplayName   string `json:"display_name"`
	ClientVersion string `json:"client_version"`
	Platform      string `json:"platform"`
}

type RegisterHostResponse struct {
	SchemaVersion string `json:"schema_version"`
	Scope         Scope  `json:"scope"`
}

// Route identifies a physical destination; a provider/model label alone is
// insufficient. Endpoint is a canonical base URL. Placement is local/external.
type Route struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint"`
	Model     string `json:"model"`
	Placement string `json:"placement"`
}

// TaskDefault retains Cercano's persisted task, destination, and quality
// vocabulary. FallbackRouteIDs is ordered and never implies additional routes.
type TaskDefault struct {
	Task                   string   `json:"task"`
	Destination            string   `json:"destination"`
	Quality                string   `json:"quality"`
	RouteID                string   `json:"route_id"`
	FallbackRouteIDs       []string `json:"fallback_route_ids"`
	AllowDeveloperOverride bool     `json:"allow_developer_override"`
}

type SkillAssignment struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	SizeBytes int    `json:"size_bytes"`
}

// Policy is the already-composed effective policy. Revision increases on a
// configuration change (including rollback); lease refresh alone keeps it.
// Empty allowed_routes means deny every model call, not inherit local routes.
type Policy struct {
	SchemaVersion        string            `json:"schema_version"`
	Scope                Scope             `json:"scope"`
	Revision             int64             `json:"revision"`
	IssuedAt             time.Time         `json:"issued_at"`
	ExpiresAt            time.Time         `json:"expires_at"`
	MinimumClientVersion string            `json:"minimum_client_version"`
	AllowedRoutes        []Route           `json:"allowed_routes"`
	TaskDefaults         []TaskDefault     `json:"task_defaults"`
	Skills               []SkillAssignment `json:"skills"`
}

// SignedPolicy carries unpadded base64url payload and signature. The signature
// covers "cercano-enterprise-policy-v1\n" followed by the exact decoded payload
// bytes, using Ed25519. Never parse and reserialize before verification. Trusted
// key discovery, signature verification, and key rotation are later milestones.
type SignedPolicy struct {
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type EffectivePolicyResponse struct {
	SchemaVersion string       `json:"schema_version"`
	Policy        SignedPolicy `json:"policy"`
}

type SkillContentResponse struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Version       string `json:"version"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Content       string `json:"content"`
}

// SyncAcknowledgement reports only a fully activated bundle. It is an
// observation by the client, not proof of device integrity. Received-at time
// and the authenticated membership/host binding are supplied by the server.
type SyncAcknowledgement struct {
	PolicyRevision int64             `json:"policy_revision"`
	ClientVersion  string            `json:"client_version"`
	Skills         []SkillAssignment `json:"skills"`
}

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}
