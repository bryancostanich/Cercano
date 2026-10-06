# Security operations gate — root policy approved; remaining choices pending

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

## Remaining decision queue, one at a time

- Metadata hosting, publication preconditions and retained historical metadata.
- Concrete signing-job permissions and emergency-recovery review before provisioning.

Read-only GitHub inspection confirmed the existing Cercano repository is public
and has no Pages site enabled. Proposed next choice: host signed metadata on
GitHub Pages in this same repository, retaining application archives in its
existing GitHub Releases. Alternative: a separately provisioned object-storage
endpoint/CDN for metadata. No separate public release repository is proposed.
Pages would require explicitly enabling a site and tightly scoped deployment
permissions; this inspection did not change those settings. Hosting selection
is still pending.

Short fixture lifetimes, one-key test role thresholds and test storage
preconditions are not production defaults. Native runner access and production
provisioning/publication remain explicit authorization gates.
