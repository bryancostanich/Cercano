# Standalone Cercano macOS Release

## Problem and motivation

Cercano needs a supported installation and release path that does not depend on a development checkout. The current development build and launcher are not a production distribution contract. The first release must establish trusted macOS binaries, repeatable publishing, Homebrew installation, and safe behavior when the installed binaries are upgraded while an agent is running.

## Goals

Ship the standalone agent and terminal client, `cercano` and `cercano-cli`, for macOS on Apple Silicon only. Both executables must carry the same release version and work outside the source tree.

Build, sign, notarize, and publish versioned release archives through GitHub Actions. Use GitHub Releases in the existing Cercano source repository for public artifacts. Distribute a binary-installing Homebrew formula through https://github.com/bryancostanich/homebrew-tap.

A clean supported Mac must be able to install through the tap, invoke the installed entry points, launch and connect to the agent, and configure a model without requiring Go, a source checkout, or developer signing credentials. Model downloads and optional integrations must be clearly distinguished from base installation requirements.

Homebrew upgrades must not terminate an active agent or interrupt conversations. When a newly installed client cannot communicate compatibly with an existing agent, it must give actionable restart guidance instead of silently killing the process or failing opaquely. Preserve user configuration, credentials, and conversation databases.

## Non-goals

Intel macOS, Linux, Windows, the Mac App Store, a graphical installer, and automatic background-service registration are outside the first release. Do not bundle model weights. Do not replace the development launcher workflow or redesign runtime management unless a demonstrated installation blocker requires a separately reviewed change.

## Constraints

Use Developer ID Application signing for distributed executables, with hardened runtime and secure timestamps. The release path must fail closed if signing credentials are absent or signing, notarization, or verification fails. The permissive development signing helper may remain permissive for development builds.

Signing runs in a protected GitHub Actions release environment. Provision the signing identity into a temporary keychain, clean it up after execution, and restrict certificate, notarization, and tap-write credentials to jobs that require them. Do not expose secrets to untrusted pull-request code or logs. Verify certificate type, validity, and availability without exporting or exposing private key material during discovery; provisioning for CI is an explicit operator step.

Published archives must have stable versioned URLs and SHA-256 checksums. Sign the final executable bytes before archiving and checksum the final archives. Publish only after notarization is accepted and signatures are verified. Update the formula only after its referenced artifacts exist. Preserve published versioned artifacts rather than silently replacing their contents.

Notarization acceptance is not equivalent to stapling or a clean-machine Gatekeeper test. Raw executables and ZIP archives cannot themselves carry stapled tickets. Validate the actual chosen archive and Homebrew installation path, including first execution on a clean Mac; do not claim offline Gatekeeper support without testing it.

Do not put private keys or credentials in the source repository, formula, or release archives. License and redistribution obligations apply to every bundled component. External runtime downloads must be inventoried, but third-party executables must not be re-signed or bundled by accident.

Implementation approval does not authorize changing repository visibility, publishing a release, pushing to the tap, or enrolling/uploading credentials. Those operations require explicit authorization. No automatic agent restart is part of installation or formula upgrade hooks.

## Decisions

### Platform and installation channel

The user approved macOS Apple Silicon only, a formula in `bryancostanich/homebrew-tap`, and the two-binary standalone distribution. There is no remaining platform or formula-versus-cask fork for this effort.

### Signing and notarization location

The user approved GitHub Actions over workstation-only signing. A protected release environment and temporary signing keychain provide a repeatable publishing path without dependence on one developer workstation.

| Axis | GitHub Actions — chosen | Developer Mac |
|---|---|---|
| Setup | Release workflow, temporary keychain, scoped secrets | Release script and locally configured Keychain |
| Credential exposure | Private key provisioned to protected release job | Private key remains on workstation |
| Reliability risk | Workflow permissions and secrets can fail; exercise with a rehearsal | Workstation state can affect builds; requires clean-checkout discipline |
| Outcome | Repeatable tagged release process | Valid but workstation-dependent release process |
| Ongoing burden | Workflow maintenance and credential rotation | Operator runs each release on configured Mac |
| Verification | Signature, notarization, and clean-machine installation checks | Same checks |

### Artifact hosting

Use GitHub Releases in the existing Cercano repository, as approved by the user. A separate releases repository is not wanted. Confirm that public artifact downloads work without authentication; report a blocker rather than changing repository visibility if they do not.

| Axis | Source repository releases — chosen | Separate public releases repository |
|---|---|---|
| Setup | Publish alongside source tags | Cross-repository publication credentials |
| Source visibility | Public downloads require publicly accessible releases | Can separate private source from public binaries |
| Traceability | Source tags and artifacts are together | Must record source revision separately |
| Failure risk | Fewer cross-repository permissions | Additional permissions and destination configuration |
| Maintenance | Source repository and tap | Source repository, releases repository, and tap |

### Upgrade behavior

The user approved non-disruptive binary replacement: no automatic termination of the running agent, actionable guidance for incompatible clients, and no automatic background-service installation. Compatibility must be based on an explicit existing contract where available, not an unreviewed assumption that every version difference is incompatible. Any required new protocol contract is a design checkpoint before implementation.

## Current evidence and unresolved verification

The existing `source/server/scripts/codesign-if-available.sh` selects `CERCANO_CODESIGN_ID` or discovers a Developer ID Application identity. It deliberately skips signing when none is available and invokes `codesign --force --sign` without explicitly requesting hardened runtime or a timestamp. A release-specific strict path is therefore required.

Delegated inspection reports that both Makefiles stamp `main.version` and build the two named binaries. The CLI Makefile documents sibling binary discovery. The developer launcher is repository-dependent and must not be installed as the production entry point. These observations are not yet evidence that discovery works through Homebrew symlinks.

The broad delegated audit did not establish complete runtime asset requirements, existing workflow coverage, the actual agent/client compatibility contract, or updater behavior under Homebrew. Those items remain explicit discovery gates. Do not interpret missing audit results as proof that functionality is absent.

Before execution design is finalized, establish the minimum supported macOS version from actual build/runtime requirements; inventory embedded and installed assets and optional runtimes; inspect both directions of executable discovery through Homebrew-style symlinks; trace startup, compatibility, restart, and updater behavior; and verify the source release endpoint and tap conventions. Escalate consequential new architecture or security choices rather than silently expanding this scope.

## Acceptance criteria

A rehearsal produces macOS arm64 archives containing correctly versioned, signed executables and every required base asset. Signature verification and Apple notarization both succeed; no release artifact is accepted if either fails.

Installation tests exercise a temporary Homebrew-style prefix and symlinks outside the source tree. A real clean-Mac test verifies the formula, downloaded artifact trust behavior, initial startup, connection, and a configured-model interaction. Tests must not require credentials or large model downloads merely to run the formula smoke test.

Upgrade integration tests cover an already-running compatible agent and an incompatible-agent case. Installation does not kill either process; an incompatible connection produces usable restart instructions. User state is preserved, and Keychain access is checked across signed upgrades. Homebrew-managed installs must not be silently overwritten by a conflicting self-update path.

Release failure tests or controlled rehearsals establish that missing signing identity, notarization rejection, and unavailable artifacts prevent publication or formula promotion as appropriate. Validate archive checksums and the formula's references before promotion.

Document operator credential provisioning, release invocation, installation, first-run model setup, manual restart after upgrades, development-launcher PATH conflicts, troubleshooting, and recovery from a failed release. Public publication and tap push remain separately authorized final steps.
