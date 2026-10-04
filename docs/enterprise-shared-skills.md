# Organization shared skills

An administrator publishes text-only skills alongside a model policy. The host
verifies and activates the complete bundle, then gives each new turn a fixed copy
of its assigned skills. Publishing an update does not change instructions halfway
through work. The next turn receives the new versions. Removing a skill removes
it from the next turn's catalog; rolling back a publication restores the assigned
older version in a new policy revision.

## What developers and agents see

Shared skills have IDs such as `enterprise/ORGANIZATION-ID/review`. They cannot
replace built-in skills or workflow protocols, even if they have the same title.
The catalog identifies the source as `enterprise` and includes the assigned
version. Built-in entries identify their source as `builtin` and version as
`bundled`.

The main agent sees each assigned skill's ID, title, description and version in
its turn instructions. It reads the full text when needed with `get_shared_skill`.
Delegated agents inherit the same snapshot and receive the same read-only tool.
The tool returns text and metadata; it does not run scripts, install files, or
grant additional model or tool permissions. Personal skill files are unchanged.

The turn instructions explicitly replace earlier skill assignments in conversation
history. Old conversation text is not deleted. When no shared skills are assigned,
the prompt says so instead of silently leaving an earlier assignment in effect.
As with other written instructions, a model's compliance with skill prose is not
a deterministic security boundary. Model restrictions remain enforced in code.

External MCP clients can use `cercano_skills` with `action: "list"` or
`action: "get"` and the exact namespaced ID. Each request reads the host's current
verified bundle and includes source/version metadata. An MCP client controls its
own outer conversation; Cercano does not claim to pin that client's entire turn.

## Host and worker behavior

A managed worker receives the complete pinned policy and skills over the private
host-worker stream. It checks assignment IDs, versions, byte lengths and content
digests before building providers or running tools. Enterprise credentials never
enter this snapshot. A dedicated settings-aware RPC prevents an older worker from
silently ignoring the new fields. Missing or incomplete settings stop the turn.

The snapshot does not authorize model requests. Every physical model attempt,
including retries, still asks the host's current policy authority. Revocation or
expiry can stop work even when its pinned instructions are older.

The existing signed-bundle download/cache logic remains responsible for trust,
complete activation, restart behavior and synchronization acknowledgements. Skill
discovery also requires a usable managed connection, so revoked members cannot
continue reading cached assignments through the catalog RPCs.

## Verification and remaining work

Automated tests exercise signed updates/removal/rollback, snapshot mutation
isolation, cross-organization lookup, source/version discovery, MCP formatting,
main-worker tool execution, delegated-agent tool execution, malformed bundles,
and rejection of workers that only support the earlier authorization protocol.
All use disposable local data; they do not contact Google or real model services.

Managed routing defaults and ordered fallback selection remain separate work.
Carrying the policy in a turn snapshot does not yet make the routing layer apply
those choices. The administration interface and complete two-customer demonstration
are also still pending. See the [implementation status](enterprise-implementation-status.md).
