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

## Remaining decision queue, one at a time

- Specific role permissions, expiry/renewal periods and emergency recovery.
- Metadata hosting, publication preconditions and retained historical metadata.

Next proposed expiry policy (not yet approved): timestamp valid for seven days,
snapshot for thirty days, targets for ninety days, root for one year. Scheduled
automation runs daily to renew online metadata as needed, without changing
approved target contents or root trust. Notify early for offline root renewal
(starting ninety days before expiry), alert on missed renewal/failure, and
verify metadata freshness independently of whether a scheduled run starts.
GitHub scheduling alone is not a guarantee of timely renewal.

The tighter alternative is a one-day timestamp with renewal every six hours,
retaining the same longer-lived roles. It narrows stale-update-information
exposure but gives less grace for scheduler, signing or hosting outages. In
both cases, expiration blocks new updates rather than installed application
use, and there is no ignore-expiry switch. Routine renewal must verify current
metadata and targets and must not blindly re-sign arbitrary remote content.

Short fixture lifetimes, one-key test role thresholds and test storage
preconditions are not production defaults. Native runner access, production
provisioning/publication, concrete signing-job scopes and emergency recovery
remain explicit gates.
