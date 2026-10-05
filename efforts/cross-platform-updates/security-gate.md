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

## Remaining decision queue, one at a time

- Routine signing: online targets/snapshot/timestamp signing in release CI,
  versus manually approved offline targets signing with online freshness roles.
- Specific role permissions, expiry/renewal periods and emergency recovery.
- Metadata hosting, publication preconditions and retained historical metadata.

Next recommendation: use dedicated online role keys for routine releases in
GitHub Actions, never the root key. This permits existing release automation to
publish updates without another offline signing ceremony. Separate keys limit
which role a particular job should receive, but keys accessible to the same CI
security boundary are not independent protection from compromise of that
boundary. Freshness renewal must be scheduled even without new app releases.

The strongest alternative is offline targets signing for each authorized new
release, with online timestamp/snapshot renewal. That reduces the ability of
compromised release CI alone to authorize new executable content, at the cost of
manual signing and handling targets expiration when no release is made.

Neither routine-signing alternative is approved yet. Short fixture lifetimes,
one-key test role thresholds, and test storage preconditions are not production
defaults. Native runner access and production provisioning/publication remain
separate authorization gates.
