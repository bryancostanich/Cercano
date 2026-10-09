# Using an enterprise account

Enterprise commands now control the running Cercano host. The host owns sign-in,
Keychain credentials, policy refresh and model authorization. The command-line
client does not hold a second copy of the connection or rotate its credentials.

This increment activates **model restrictions and shared skills**. Main and
delegated agents discover assigned skills and read the pinned versions through
a read-only tool. See [shared skills](enterprise-shared-skills.md). Applying
administrator routing defaults and ordered fallbacks is also implemented; see the
[implementation status](enterprise-implementation-status.md).

## Start and connect the host

The V1 account-management pilot supports macOS. Your administrator provides a
trusted HTTPS service origin and organization UUID. Accept the organization's
invitation in the browser before connecting Cercano. The service needs Google
OAuth, a signing key and a published policy.

Use a build with a numeric version accepted by the policy. For local development:

```sh
cd source/server
go build -ldflags '-X main.version=1.0.0' -o bin/cercano ./cmd/cercano
bin/cercano agent
```

In another terminal, using the same build:

```sh
bin/cercano enterprise login --server https://enterprise.example --organization YOUR_ORGANIZATION_UUID
bin/cercano enterprise status
```

Replace the example origin and organization UUID with the administrator's values.
For a non-default local host, append `--address 127.0.0.1:PORT` to each enterprise
command. Commands do not start or upgrade a host automatically. An older host
returns an update-required error instead of creating an unenforced connection.
The ordinary `dev` version does not satisfy a numeric minimum version.

Login opens the system browser from the host. Google sign-in returns a one-use
code to a temporary loopback listener. The host exchanges it using PKCE, enrolls
this device, saves credentials in the separate `cercano-enterprise` Keychain
service, and verifies the policy and skills. Provider API keys remain in their
existing store.

## Understand status and synchronization

`status` prints the organization and team, applied policy revision, authorization
deadline, and synchronization state. It distinguishes an unassigned team from
missing membership details. During an outage or blocked connection, displayed
membership describes the last applied policy, not proof of current access.

Use `cercano enterprise status --json` for these machine-readable host fields:

- `managed` means the host is using an enterprise profile.
- `enforcement_active` means the model-request gate is installed for that profile.
  It remains active when work is blocked.
- `connected` means the host has saved connection credentials.
- `usable` means a verified policy is currently within its authorization lease.
- `membership_known`, `organization_name`, `team_id`, and `team_name` describe
  the last applied policy response. An absent team with known membership means
  organization policy applies without additional team restrictions.
- `revision` and `valid_until` identify the accepted policy and its deadline.
- `changing` means a connection change, such as browser sign-in, is in progress.

When synchronization fails, the host sends a best-effort report containing only
a fixed error code and client version. It never sends raw error text, local paths,
prompts, source code, or provider credentials. Reporting has a two-second deadline
and cannot change whether the cached policy is usable. Older servers may ignore
it; offline and revoked clients may be unable to report. The administrator sees
the last successful acknowledgement until a complete bundle is applied again.

The host checks about once a minute, with bounded backoff after failures.
Unchanged responses reuse the verified bundle without extending its lease.
When fewer than two minutes remain, the host requests a fresh signed policy.
Sign-in, restart and recovery after a failed check also require a full response.
To request synchronization immediately:

```sh
bin/cercano enterprise sync
```

For an account saved by the earlier connection preview, `sync` explicitly
activates that account in the running host. A synchronization error can leave the
host managed and blocked; use `status` to inspect it and retry when service or
Keychain access is restored.

A temporary outage retains an already verified bundle only until its original
lease deadline. A denial or failed verification blocks work immediately. A
restart always requires fresh online verification; the disk cache cannot grant
permission to run work. A restriction published during a turn applies before
the next physical model request, including a retry or fallback.

## Inspect the settings you can use

```sh
bin/cercano enterprise policy
bin/cercano enterprise skills
bin/cercano enterprise skills --skill enterprise/ORGANIZATION_UUID/SKILL_SLUG
```

`policy` lists the allowed provider, endpoint, model and placement for each route,
then the task defaults, ordered fallbacks and whether each choice is locked.
`skills` lists enterprise assignments with their source, namespaced IDs and exact
versions. Pass one of those IDs with `--skill` to read its text. These commands
read the running host's verified bundle; they do not fetch credentials or start
model requests. Personal and repository skills remain available through the
ordinary skill catalog.

Append `--json` to get structured output. Policy and assigned skill inspection
require a usable managed bundle. If access is blocked or expired, use `status`
and `sync` to diagnose and recover. An inspection is a snapshot, not permission
for future inference: each actual request still checks current authorization.

## Sign out or return to personal settings

```sh
bin/cercano enterprise logout
```

Logout removes local credentials and attempts server revocation. It leaves the
host in a managed, blocked state, including after restart. If remote revocation
fails, the command reports that failure even when local removal succeeded. A
locked Keychain must be unlocked before removal can complete.

To deliberately return to personal settings after logout:

```sh
bin/cercano enterprise standalone
```

This is a separate, explicit mode change. It is rejected while credentials remain
connected or work is active. Login and logout also require active work to finish.
Personal model settings are preserved. A new login after logout enrolls a new
host record.

## One connection owner

The host holds the enterprise connection lock for its lifetime. Another process
cannot rotate the same refresh credential or clear its managed profile. A host
that was already running standalone notices another host's activation and blocks
its own model requests rather than silently bypassing that profile.

Use the normal host connection for managed MCP access. An independently embedded
MCP host cannot become a second enterprise connection owner. Its model requests
remain blocked while the main host owns the connection. Restart a secondary host
or explicitly select standalone there after the owner has logged out and released
the connection.

## Verification and local state

The trusted service origin supplies Ed25519 verification keys. The host rejects
redirects, bad signatures, unknown policy schemas, wrong organization/member/host
scope, older revisions, expired policies and unsupported client versions. Every
skill must match the signed ID, version, length and SHA-256 digest. The bundle is
accepted only after all downloads and persistence succeed.

`~/.cercano/enterprise/active-bundle.json` stores policy and skill content with
owner-only permissions. It contains no credentials and is not trusted after a
restart. The `managed` marker in that directory preserves the mode even when
Keychain credentials disappear. Only an explicit standalone command clears it.

Tests use disposable TLS servers, real signatures, an in-memory credential store
and local gRPC. They cover browser callback and PKCE exchange, policy enforcement,
updates, logout, restart, credential loss, lock ownership and explicit standalone
mode. They do not sign into a real Google organization or modify your Keychain.
Live Google and Keychain acceptance testing remains outstanding.
