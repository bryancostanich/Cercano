# Security operations design gate — approved; production provisioning still gated

The local go-tuf/v2 v2.4.2 proof is feasible and all test keys were ephemeral.
No production private keys, signer credentials, endpoint or expiration policy
have been created. This document records decisions, not provisioning authority.

## Approved root policy

The user rejected two-of-three root-key custody as too much overhead for now,
then explicitly approved **one offline root key with an encrypted offline
recovery backup**.

- The root private key stays out of CI and out of the application.
- Root operations authorize trust-policy changes and key rotation, not each
  ordinary application release.
- The recovery backup must be offline and encrypted; recovery must be rehearsed
  before production enrollment. Encryption-secret custody remains part of that
  operational procedure, not a hardcoded value or CI secret.
- A backup protects availability, not signature-threshold security. Compromise
  of any usable copy permits root changes; loss of all copies prevents normal
  trusted root rotation.
- A later threshold policy can be introduced through a signed root rotation
  while the current key remains available and trusted. This is not an automatic
  response to a lost/compromised root key and must not be advertised as one.

The alternative, two-of-three independently stored keys, would improve
single-key-compromise/loss tolerance but imposes custody and signing overhead
that the operator declined for the initial rollout. This choice is settled;
implementation must not silently adopt a threshold or put root into CI.

## Approved routine signing policy

The user explicitly approved automatic signing in GitHub CI. Dedicated online
keys authorize the targets, snapshot and timestamp roles. The root remains
offline; ordinary application releases do not require the root key or an
offline signing ceremony.

Separate role keys let jobs receive only the authority they need, but keys
accessible within the same compromised CI security boundary do not provide
independent protection. A compromised CI job with targets-key access can
authorize malicious update contents. Root custody and a rehearsed revocation/
rotation procedure remain important, but cannot undo code already installed.

The alternative of offline targets authorization was considered for limiting
CI compromise impact; its per-release ceremony was not chosen. This decision
is settled. It authorizes the architecture, not production secret provisioning
or an unreviewed expansion of repository permissions.

## Approved expiry and renewal policy

The user approved the following after clarifying that ordinary renewal is
automatic, not a weekly manual task:

- Timestamp validity: seven days.
- Snapshot validity: thirty days.
- Targets validity: ninety days.
- Root validity: one year; offline renewal reminders begin ninety days before expiry.
- Online metadata renewal/checking: daily automation, even without new app releases.
- Alert on failed/missed renewal and monitor freshness independently of whether
  a scheduled Actions run starts. Scheduling alone does not guarantee execution.

Renewal must verify retained metadata and authorized target contents before
signing. It must not authorize arbitrary replaced remote content or change root
trust. Expiration blocks new updates, never ordinary use of the installed app;
there is no ignore-expiry switch. A faster one-day/six-hourly freshness policy
was considered but not selected because it gives less operational recovery time.
Offline root renewal is an occasional manual operation and does not inherently
require generating a new root identity.

## Approved hosting: shared GitHub Pages website

The user approved GitHub Pages in the existing Cercano repository and specified
that it will also host the website. Reserve `updates/tuf/` within the site's
published output for signed metadata; this is machine-readable static content,
not a generated HTML page. Application archives stay in existing GitHub Releases.
No separate public release repository or hosting provider is introduced.

The base site URL must be configurable: a project Pages site can have a repository
path prefix, and a later custom domain can change the origin. Do not assume the
website is hosted at an origin root or apply HTML rewriting to signed JSON.
Preserve byte-identical immutable numbered roots/versioned metadata and replace
only the authorized mutable metadata pointers. Website deployment must merge the
current feed into its complete Pages artifact, while feed renewal must preserve
the current website. Neither workflow may deploy a subtree alone as the site.
They must share publication coordination so a stale site build cannot roll back
feed freshness or delete a root needed by offline clients.

Read-only inspection found Pages disabled before approval. Production enabling,
DNS/domain changes, signer provisioning and deployment permissions have not been
performed. The separate-storage alternative was not selected.

## Remaining gate before production provisioning

The user approved the concrete signing-job and emergency-recovery profile in
signing-operations.md. The security-design gate is complete; fixture-based
implementation may continue. The approved profile is: signing jobs receive only
required online role keys; root never enters CI. The Pages deployment step needs
only the approved site artifact and deploy authority, not signing keys. Renewal
re-verifies current signed metadata and target identities before retaining target
authorizations; no arbitrary remote files are blessed by a timer. Define release,
renewal and website publication coordination before granting write authority.

Emergency response must distinguish a broken renewal job from compromised role
keys: restore renewal for availability errors, but disable compromised signing
and perform offline-root-authorized role-key rotation for trust errors. Do not
bypass verification, re-sign suspect targets or claim rotation undoes previously
installed malicious code. This policy review does not require provisioning now.

Short fixture lifetimes, one-key test role thresholds and test storage
preconditions are not production defaults. Native runner access and production
provisioning/publication remain explicit authorization gates.
