# Initial signing operations — approved profile; provisioning remains gated

Approved architecture: single offline root plus encrypted offline recovery
backup; automatic GitHub CI routine signing; timestamp7d/snapshot30d/targets90d/
root1y; daily renewal; GitHub Pages shared with the website. No production keys,
secrets, Pages configuration or privileges have been created in this effort.

The user explicitly approved this least-privilege execution profile. It does
not change the already-approved trust or hosting choices. Implementation and
fixture testing may proceed; production provisioning is still separately gated.

## Roles and job isolation

Use distinct online keys for targets, snapshot and timestamp, with one authorized
signature required per online role initially. Never copy root private material
into Actions or the application. This is separation of role authority, not a
claim that keys sharing CI are independent compromise boundaries.

Release and renewal signing jobs receive only the role keys required for their
metadata. They run from trusted default-branch/release workflow code, never from
untrusted pull-request code. Application artifacts must pass release verification
before they become new targets. Renewal verifies retained signed metadata,
archive identities and policy and does not discover/authorize arbitrary new
content from a remote directory listing.

Separate signing from deployment: the Pages deploy job receives the complete
site artifact, not private signing keys. Restrict the deployment job to the
necessary `pages: write` and `id-token: write` permissions, with read-only source
access where required. Default workflow permissions remain read-only. Any job
that writes a metadata-state branch needs explicitly scoped `contents: write`;
GitHub's repository token does not enforce per-path write scope, so do not claim
it does. Generated-state paths, expected source revisions and normal non-force
publication must be checked, and write authority must not be granted merely to
read a feed. No broad PAT is implied by this proposal.

Website publication, feed renewal and release publication must share one
coordinator/freshness gate. At publication time re-read the current authorized
feed state, retain required historical metadata, and refuse a stale deployment.
A whole-site build must not overwrite fresh feed metadata with its earlier
snapshot. Validate this behavior natively against Pages before production use.

## Monitoring and recovery

Daily renewal is automated, with alerts on signing/publishing failures and
missed renewal. Monitoring must check the actual public timestamp expiry rather
than infer health from a workflow's existence. An independent monitoring trigger
must be chosen/provisioned before production; another schedule subject to the
same disabled-scheduler condition cannot prove it will detect that condition.

On an ordinary automation failure, repair/retry the verified publication path;
do not bypass expiry and do not stop the installed application. On suspected
online-key compromise, stop the affected signing/publication path, review target
contents, replace role keys using offline-root-authorized rotation, publish the
required root history, and then resume with reviewed artifacts. Key rotation
does not undo code already installed. Escalate a suspected root compromise to an
explicit incident-response decision rather than pretending routine renewal is
safe recovery.

Remind the operator about offline root renewal starting ninety days before its
one-year expiration. Rehearse recovery using an encrypted backup with test keys
before enrolling clients; never test recovery by exporting production root keys
into CI or logs. Retain root history needed by offline clients.

## Authorization boundary

The approved execution profile permits implementing and testing these boundaries
with fixture keys. Actual private-key generation/custody, secret installation,
enabling Pages, monitoring subscriptions or external services, and the first
production feed publication remain separately authorized actions under the
approved plan. No production permission grant follows from profile approval.
