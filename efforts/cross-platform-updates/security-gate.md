# Security operations gate — awaiting operator selection

The local go-tuf/v2 v2.4.2 proof is feasible and all test keys were ephemeral.
No production root, threshold, signer, endpoint or expiration policy was created.
The run has reached the explicit Phase 2 approval boundary.

Decision queue, one at a time:

- Root-key custody and root-signature threshold.
- Online signing role separation, permissions, expiry/renewal and emergency recovery.
- Metadata hosting, publication preconditions and retained historical metadata.

First recommendation: two signatures from three independently stored offline
root keys. This tolerates one lost/compromised root key, provided custody is
actually independent. Alternative: one offline root identity plus encrypted
offline recovery backup; simpler for a solo maintainer, but any compromised
usable copy can authorize root changes. Both keep root keys out of normal CI.
Root operations are infrequent; this proposal does not require two manual root
signatures for every ordinary application release.

No selection has been made. Do not mistake the one-key thresholds and short
lifetimes used by proof tests for production defaults. The next implementation
slice must follow operator approval of this gate and the remaining security
operations choices. Native runner access and production provisioning/publication
are separate future authorization boundaries.
