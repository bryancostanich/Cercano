# Enterprise connection preview

The macOS CLI can sign into a Cercano Enterprise account, enroll this client,
and download a verified policy and its assigned skills. Credentials are kept in
a separate macOS Keychain service called `cercano-enterprise`. Provider API keys
stay in their existing store.

**This is a connection preview. It does not enforce model restrictions or install
skills into the running agent.** The commands print `enforcement_active: false`.
Normal Cercano inference behavior is unchanged. Runtime enforcement and the
managed-profile interface are the next implementation steps.

## Try the preview

Your administrator must provide a trusted HTTPS service origin and organization
UUID. First accept your organization's invitation in the browser. The service
must have Google OAuth and policy signing configured and a policy published.

Use a build with a numeric release version that satisfies the policy's minimum
client version. For a local development check, set it explicitly:

```sh
cd source/server
go build -ldflags '-X main.version=1.0.0' -o bin/cercano ./cmd/cercano
bin/cercano enterprise login --server https://enterprise.example --organization YOUR_ORGANIZATION_UUID
```

Replace the example origin and organization UUID with your administrator's
values. The ordinary `dev` version intentionally does not satisfy a published
minimum version. No development option bypasses signature or policy validation.

Login opens the system browser. Once Google verifies your account, the browser
returns a short-lived code to a temporary loopback listener. The client exchanges
that code over HTTPS using PKCE, enrolls its host and saves its credentials in
Keychain. The callback never carries an access or refresh credential.

The login command then fetches the policy and skills. If synchronization fails
after login, the connection may already be saved; run `status` and then `sync`
when the server is available. A successful connection is not evidence that
runtime model enforcement is active.

```sh
bin/cercano enterprise status
bin/cercano enterprise sync
bin/cercano enterprise logout
```

`status` reports whether credentials are saved. `sync` performs a fresh online
verification and reports the accepted revision and lease expiry. Each command
runs in a new process, so a later `status` reports `usable: false` until that
process verifies a bundle online. The preview does not read a disk cache as
permission to run work. The `usable` field refers only to the verified bundle
inside the current process, not to the running agent's enforcement status.

Only one enterprise connection is saved per local OS account. Log out before
signing into another organization. Concurrent enterprise commands are rejected
to prevent credential rotation and connection changes from racing. A new login
after logout enrolls a new host; host-list cleanup is not part of this preview.

Logout blocks local use, attempts server revocation, and removes saved
credentials and the cache. If the server cannot be reached, local credentials
are still removed and the command reports the failed remote revocation. A
locked Keychain can prevent removal; unlock it and retry.

## What synchronization verifies

The client obtains Ed25519 public keys from the configured HTTPS origin. It
rejects redirects, invalid signatures, unknown policy schemas, wrong customer,
member or host scope, expired policies, older revisions and unsupported minimum
client versions. The service origin is the trust anchor; the client does not
accept a key URL supplied by a policy.

Every assigned skill must match the ID, version, byte count and SHA-256 digest
in the signed policy. The client accepts the new bundle only after every download
has passed validation, a complete cache file has been written atomically, and
the highest accepted revision has been saved in Keychain. An incomplete download
cannot mix a new policy with old skills.

The cache is `~/.cercano/enterprise/active-bundle.json`, written with owner-only
file permissions. It contains policy and skill content, not credentials. It is
never treated as trusted authorization after a restart. Restarting requires
online revalidation, even if the previous lease would otherwise still be valid.

In a long-running Manager, a temporary network outage retains an already
verified bundle only until its original deadline. Authorization denials and
verification failures block it immediately. The library supports background
refresh about once a minute, with bounded backoff, but these preview commands
do not start a background service or attach the Manager to the normal host.

Once a bundle has been accepted, the client reports its revision and exact skill
manifest to the service. That report means the bundle was verified and accepted;
it does not assert that model enforcement is active. A publication race can
reject the report; another sync obtains the latest policy.

## Remaining integration work

The [runtime authorization boundary](enterprise-runtime-enforcement.md) now covers
model transports and managed worker requests, but is not yet installed by the
host. The host still needs to own this connection for its lifetime, expose connection
status through its normal interfaces, and apply policy before every inference
attempt, including retries and fallbacks. Turn execution must use a consistent
skill snapshot while also checking the latest model restrictions. Local managed
profile settings must not override administrator restrictions.

The library already offers `Begin` to hold a turn's bundle, `Current` to obtain
currently valid policy, and `Run` for periodic refresh. A future host integration
must hold the connection lock for the host's lifetime and route connection
changes through that owner. These APIs alone do not provide model enforcement.

## Validation

The tests use a local HTTPS server, real signatures, and an in-memory credential
store. They exercise PKCE, incorrect scope and signatures, interrupted skill
downloads, credential persistence failure, revision rollback, lease expiry,
restart behavior, logout, redirects and concurrent connection ownership. Tests
do not write to your Keychain or sign into a real Google Workspace organization.
A live Google and Keychain acceptance check remains outstanding.
