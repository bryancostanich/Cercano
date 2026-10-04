# Managed model routing

When a developer works in an enterprise account, the running host uses the
administrator's published task defaults. Personal configuration stays on disk
and becomes active again only after an explicit return to standalone mode.

## What an administrator chooses

Each task assignment names an approved route, a quality level, a destination,
and an ordered list of fallback routes. A route identifies the provider,
endpoint, model, and whether inference is local or external. An assignment can
also allow the developer to override its defaults.

The supported tasks are chat, compaction, dispatch, reconnaissance, mechanical
development, investigation, implementation, review, research, git land, and
watchdog. A task without an assignment stops with a clear configuration error.
It does not silently inherit personal settings. An administrator should assign
every task their developers need.

Explicit local co-processor tools use the dispatch assignment and require a
local default. These tools remove external routes from their fallback list.
Their promise to run locally continues to apply in managed mode.

## How developer preferences work

Locked assignments use the administrator's model and quality even when personal
settings name another model or destination. A request that explicitly names a
different model is rejected.

For unlocked assignments, saved task preferences and explicit model choices may
select another route in the approved list. They cannot add personal backup
providers, change the approved endpoint, or introduce an unapproved model. A
model-only override stays on the administrator's endpoint. Quality changes
control the existing quality budgets; changing only quality does not replace an
explicit administrator model with a shipped model recommendation. When a saved
task route is selected, its quality selects the personal profile's tier model,
which must itself be approved. The legacy local compaction model override is
honored only for an unlocked local compaction assignment.

The host pins policy defaults and the developer routing snapshot for a turn and
its nested calls. Settings published during that turn become the defaults for
new work. Restrictions still take effect before the next physical request,
including within an existing turn.

## Credentials and fallbacks

The client reuses an existing credential profile only when its physical provider
and endpoint match the approved route. It substitutes the approved model without
editing that profile. A missing matching profile produces a configuration error.
The enterprise service does not distribute provider keys.

The existing retry engine handles transient failures. After an eligible failure,
managed routing tries only the administrator's listed fallbacks, in order. It
never adds personal backups. Policy denials remain terminal. After streaming
has begun, failures are surfaced without replaying partial output on another
provider.

Each fallback must support the request's tools and have a known context capacity
large enough for the complete request. The client does not truncate history to
make a smaller fallback fit. Cloud capacity comes from the existing scoped model
metadata; unknown capacity prevents fallback. Local llama-server capacity comes
from the prepared serving process. Usage and result metadata identify the model
and placement that actually served the request.

Managed Bedrock profiles need an explicit region or endpoint so the client can
match credentials before building the adapter. Subscription profiles use their
provider's fixed endpoint. Local supervised runtimes still require approval for
the actual serving endpoint and loaded model at the transport boundary.

## Verification and remaining work

Tests cover locked and unlocked preferences, missing assignments, turn pinning,
ordered fallbacks, local-only calls, revocation before retry or fallback,
streaming failures, capacity checks, and placement attribution. Main-loop,
delegation, compaction and worker tests exercise their respective integration
points. A real worker provider-assembly test uses disposable HTTPS servers and
fixture credentials to prove that personal models and backup profiles receive
no requests when managed routes are selected.

These tests complement the signed-policy and host authorization tests described
in [runtime enforcement](enterprise-runtime-enforcement.md). The administration UI and
two-organization workflow are now implemented. The workflow checks real streamed
requests, explicit model denials, retries and approved fallback against a local
model fixture. Live Google and macOS Keychain acceptance remain separate checks.
