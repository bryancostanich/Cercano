# One-shot executable/bootstrap contract — approved

The user explicitly approved the recommended private update mode in the existing
agent binary. The common authority/lifetime boundaries below are the implementation
contract. The separate-executable alternative is retained only as decision history.

The service proposal remains rejected. Both options here are short-lived
utilities, using the existing agent connection and no new listening endpoint.
The approved Phase5 plan explicitly reserves the executable/bootstrap contract
for review before installed-file activation work.

Recommended: provide a private update mode in the existing agent executable.
Dispatch it before normal agent/model/profile initialization. Verify the chosen
installed image and its supported helper mode, copy it to a protected
operation-specific temporary location outside every removable version directory,
verify the copy, then launch independently. Do not invoke unknown flags on an
older binary and assume it will behave as a helper. This retains the existing
two-binary artifact/signing inventory. Its costs are a larger temporary copy and
sharing the agent image's startup/dependency surface; isolated entrypoint tests
must prove it does not start an agent or load unrelated credentials/models.

Alternative: ship a dedicated minimal updater executable. This narrows recovery
startup dependencies but adds a third artifact with signing/packaging, delivery,
compatibility and self-replacement obligations. It is not a daemon either.

Proposed common authority/lifetime boundaries:
- Initial self-managed installation scope remains per-user. No privilege increase
  or always-privileged component; package elevation uses normal OS mechanisms.
- The updater is the sole application code path that changes active-version
  selection and activation journals after policy/trust/ownership validation.
  Launchers read selection; clients submit validated operations and read status.
  This is a code-path contract plus per-user filesystem protection, not a claim
  that other programs with the same user's full rights cannot modify those files.
- User-writable records do not authorize arbitrary privileged package commands.
  Supporting package installers must cooperate through the approved ownership
  contract, not independently overwrite self-managed files.
- The running temporary utility is outside directories it replaces/cleans.
  Temporary executable cleanup happens after exit, never by assuming Windows
  permits deletion of a running image. No user data is part of version cleanup.
- Updater schema compatibility, prior-selection recovery and helper image
  selection must be tested explicitly. This approval authorizes implementation
  and isolated tests, not speculative migrations or changes to live installations.

The private entrypoint and temporary-copy bootstrap are approved for implementation.
Normal startup must not enter update mode implicitly, and legacy binaries must
not be probed with an unknown execution flag that could start an agent.
