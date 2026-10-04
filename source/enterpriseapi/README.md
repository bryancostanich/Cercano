# Enterprise API contract (draft v1)

This independent Go module contains public wire types, a JSON Schema for the
effective policy payload, and shared compatibility examples. It introduces no
enterprise behavior into the existing Cercano host. It does not authenticate
users, verify signed policies, or enforce model restrictions.

Run `go test ./...` from this directory. The module deliberately has no agent
dependencies. The new enterprise repository consumes this exact module through
a development Go workspace until the contract has been reviewed and published.

## Proposed service operations

All organization and host selectors must be checked against verified identity
and current membership. They never establish authority by themselves.

| Method and path | Request | Response |
| --- | --- | --- |
| GET /v1/memberships | authenticated identity | MembershipsResponse |
| POST /v1/organizations/{org}/hosts | RegisterHostRequest | RegisterHostResponse (201) |
| GET /v1/organizations/{org}/hosts/{host}/policy | optional prior revision | EffectivePolicyResponse |
| GET /v1/organizations/{org}/skills/{skill}/versions/{version} | assigned skill ID | SkillContentResponse |
| PUT /v1/organizations/{org}/hosts/{host}/applied | SyncAcknowledgement | empty (204) |

These routes are specifications, not implemented HTTP endpoints. Skill IDs are
namespaced opaque identifiers and must be URL-encoded as one path segment.
Policy refresh returns a newly signed lease even when its revision is unchanged;
a 304 response alone cannot renew authorization. Repeated identical applied
acknowledgements are idempotent. The server supplies receipt timestamps.

Error bodies use ErrorResponse with bounded codes: unauthenticated (401),
forbidden (403), not_found (404), conflict (409), invalid_request (400),
unsupported_version (426), rate_limited (429), and unavailable (503). Messages
must not expose credentials or another tenant's resource existence. Cross-tenant
resource lookups return not_found.

## Policy rules

An effective policy has already combined organization and team restrictions.
Empty allowed_routes means deny all. Defaults and their ordered fallbacks must
reference approved routes. Local destination defaults cannot cross to external
inference. Existing task/destination/quality names are retained; this module
does not replace Cercano's resolver or determine its runtime eligibility.

Only explicitly unlocked defaults can be changed by a developer. An override
never permits a route outside allowed_routes. Team ceilings, run pinning, and
physical-attempt checks belong to later implementations, not this validator.

Published policy revisions are positive and strictly increase on changes,
including rollback. Refreshing a lease does not change its configuration
revision. Clients must separately enforce monotonic revision and trusted-clock
rules. A lease cannot exceed 15 minutes. Minimum client versions use stable
major.minor.patch releases; prerelease negotiation is not supported in v1.

The payload schema describes JSON shape. DecodePolicy enforces that schema and
rejects duplicate keys, invalid UTF-8, and unknown or incorrectly cased fields;
Policy.Validate additionally checks references, scopes, lease
times, endpoints, and skill conflicts. Neither is a signature verifier. Before
using a policy, a client must verify the signed envelope and check minimum
client version, revision state, and host binding. Unknown fields currently fail
closed: incompatible extensions require a negotiated schema version.

SignedPolicy specifies Ed25519 over a domain separator plus the exact payload
bytes. Payload and signature use unpadded base64url. Key discovery and rotation
are intentionally unimplemented until the authentication/publishing milestones.
No arbitrary key supplied alongside a response may become trusted implicitly.

Endpoints are exact canonical base URLs: no credentials, query, fragment,
encoded path, or trailing slash. Hostnames are lowercase. Local inference uses
a literal loopback IP; external inference requires HTTPS. This is syntax
validation, not SSRF protection or proof of a provider's actual destination.

Skills are text only, have organization-namespaced IDs and exact versions, and
include SHA-256 digests of the UTF-8 content bytes. Maximum sizes are 256 KiB per
skill, 2 MiB per assigned bundle, and 256 KiB per policy. Download integrity,
atomic activation, signed trust, and runtime skill discovery follow later.
