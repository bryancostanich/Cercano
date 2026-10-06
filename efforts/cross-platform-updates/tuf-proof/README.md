# Local TUF feasibility proof

Independent test module, not production code. Pinned
`github.com/theupdateframework/go-tuf/v2 v2.4.2` (published 2026-05-19), Apache-2.0.
Upstream requires Go 1.25 or newer; Cercano currently uses Go 1.26.0. Direct
proof dependencies include Sigstore signature helpers; transitive versions are
pinned in this module's go.mod/go.sum. Production modules are unchanged.

All Ed25519 keys are generated in memory per test, all repositories are local
`httptest` servers, and target bytes are inert text, never executables. No
production key, secret, repository or installation was used. Root/role thresholds
and lifetimes in the fixtures are test parameters, not an approved production
security policy.

## Evidence

18 top-level tests plus refresh-interruption subtests cover real Go TUF
verification and test-only publication/site-composition contracts. The verification
tests exercise the real Go TUF
client, not a mocked verifier:

- bootstrap and hash-prefixed verified download;
- wrong-platform target absent from authorized metadata;
- changed target rejected by a specifically asserted hash-mismatch error, not
  a vacuous missing-file error;
- wrong signer on timestamp, targets and rotated root;
- expired timestamp and root refused;
- rollback to older timestamp refused after a newer trusted cache;
- offline refresh fails without modifying the separate installed-file sentinel;
- missing timestamp/snapshot/targets during refresh and recovery after retry;
- sequential root rotations, without skipping a missing intermediate root;
- valid rotation recovery from an expired bootstrap root;
- incomplete publication fails until its immutable metadata is present;
- a modeled publication compare-and-swap permits one concurrent winner and
  refuses stale/downgrade writes;
- a shared-site composition contract preserves the website and byte-identical
  signed metadata under `updates/tuf/`, retaining historical roots across a site
  rebuild and rejecting reserved-path collisions or changed immutable metadata.

The site tests do not deploy Pages or prove edge-cache/hosting atomicity. They
model the invariant the production site deployment must uphold; concurrency and
freshness need verification with the chosen publication implementation.

`go test -race -timeout=60s ./...` and `go vet ./...` pass on macOS arm64.
Windows/Linux x64 test executables cross-compile; they have NOT been executed on
those operating systems for this effort. The publisher compare-and-swap is a
fixture storage contract, not proof that a not-yet-selected host implements it.
The offline installed-file sentinel confirms this test's separation of download
and activation; it is not production activation/recovery coverage.

## Observations that affect implementation

1. TUF supplies trust and metadata freshness, not process coordination, installer
   activation, or application-version policy. A publisher could authorize an old
   application version in newer signed metadata; the application must still
   enforce its release/version policy.
2. A withheld intermediate root prevents advancing that root chain, but clients
   may continue using still-fresh metadata authorized by the old root. Revocation
   is not instant merely because a newer root exists. Expiration and online-key
   custody therefore have user-visible security/availability consequences.
3. Timestamp metadata must be renewed on a schedule even without new releases.
   Expiry blocks updates, not use of the installed app. An excessively fast local
   clock can similarly make metadata appear expired. No unsafe verification
   bypass is proposed.
4. The client's persistent metadata and trust state need secure directory
   ownership and protected provenance. Restoring an arbitrary stale cache is not
   an acceptable rollback policy.
5. Test clock changes use upstream `UnsafeSetRefTime` solely in tests. Do not
   expose a production 'ignore expiry' or clock-override switch.
6. The default fetcher uses bounded attempts. In this proof, calling
   `SetDefaultFetcherRetry(..., 0)` accidentally enabled unlimited retries via
   the backoff library; a probe exposed the hang on expected root-chain 404.
   The corrected proof explicitly sets one attempt and bounded HTTP timeouts.
   Production needs explicit cancellation, limits and backoff rather than
   assuming zero means 'no retries'.
7. `metadata.HexBytes` has string formatting semantics. A first fixture wrongly
   double-encoded a target hash with `%x`; observed HTTP paths exposed that bug.
   Explicit `hex.EncodeToString` fixed it. The tampered-target test now asserts
   the hash error type so a 404 cannot falsely satisfy it.
8. Metadata/target URLs and application platform/channel selection remain policy
   inputs. Serving these fixtures over HTTP is for isolated testing, not the
   proposed production transport policy.

## Run

```sh
cd efforts/cross-platform-updates/tuf-proof
go test -v -timeout=60s ./...
go test -race -timeout=60s ./...
```

## Gate

The Go client is feasible for the approved scope, subject to native integration
and operational review. Production roles, signing thresholds, offline custody,
hosting, expiry/renewal and emergency recovery require operator approval before
provisioning. These tests do not resolve those choices or authorize publishing.
