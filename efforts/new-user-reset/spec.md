# Approved amendment — live developer reset

The user explicitly superseded the offline-only safety design. `cercano reset --setup` is a manually confirmed developer command and MUST work with other sessions open. Remove process/lifetime locks, legacy-process detection, shutdown/drain requirements and busy-session refusals. The user controls activity and accepts in-flight errors, stale settings writes and credential refresh races. Warn about those limitations; do not claim a global transaction or guaranteed quiescence. No further planning approval is required for this amendment.

Use the running agent when available so its configuration/providers refresh; otherwise reset locally. Keep existing sessions and all conversation/history data. Preserve downloads, installed runtimes and non-setup preferences. Keep confirmation, scoped deletion, truthful partial-failure reporting, and fixture-only verification. Actual reset of the user's installation still requires a separate request.

The historical specification below remains authoritative for the clear/preserve boundary and wizard behavior, but its exclusive-access/offline/legacy-process requirements are superseded by this amendment.

---

# Setup reset without deleting conversations or downloaded models

Status: human-approved specification. The user approved the specification after selecting setup-only reset and the terminal interface. Implementation-plan approval remains required. No implementation or destructive reset has been performed.

## Problem and motivation

Testing the new-user setup flow currently requires manually finding and clearing settings, credentials and unfinished wizard state. Running the setup wizard again does not itself remove old model choices, routing assignments, cloud profiles or stored tokens. A broad deletion of Cercano's directories would risk conversation history, preferences, installed runtimes and downloaded models, which the user explicitly wants to retain.

Provide one terminal command that resets the setup-related state and makes the next normal interactive launch begin setup from its first step. This approximates a new user on a machine that already has runtime software and model files installed; it is not a factory reset or uninstall.

## Approved scope

The command is `cercano reset --setup`. It is terminal-only for this effort; there is no interactive `/reset` command or model-callable reset tool.

Reset model and runtime selections, model-related tuning and connection settings, cloud profiles, routing, Cercano-owned authentication credentials, and prior setup answers. Restore current clean-installation defaults rather than hard-coded historical defaults.

Preserve all conversation and history data, downloaded model files, installed runtime binaries, and non-setup preferences. Restore model search paths to defaults. Files in previously configured custom directories remain untouched; users may re-add those paths afterward. Standard Cercano-managed downloads under `~/.cercano/models` remain discoverable through the default location, without preserving old model selections.

## User-visible behavior

The command must show a concise explanation of what will be cleared and what will be preserved, then require explicit confirmation before changing anything or shutting down an agent. Declining, end-of-input, invalid arguments and help must not mutate state or stop processes. An invocation without the required `--setup` scope must not silently choose a destructive reset. A noninteractive invocation must not implicitly approve itself; an unattended-confirmation flag is not required by this specification.

A successful command exits after resetting state. It does not start inference, download models, launch sign-in flows, or open the interactive UI itself. Its completion message instructs the user to launch Cercano normally for fresh setup and notes that custom model paths must be re-added if needed.

On the next ordinary interactive launch, the wizard starts at its initial step with no previous selections or rollback baseline. Subsequent interruption/resume and normal completion continue to use the existing wizard lifecycle. Abandoning the new wizard must not restore pre-reset profiles or model settings.

Retained conversations stay available after setup, even though their formerly configured provider credentials may no longer exist. Reset must not rewrite conversation records to remove historical references to those providers or models.

## Reset boundary

### Configuration

Use an explicit, audited setup-field boundary rather than deleting the complete config or replacing it wholesale with defaults.

Reset cloud profiles and their per-quality/image choices, Primary/Secondary bindings and backups, task assignment overrides and destination redirects, locus choice, local model selections and overrides, user cloud model recommendation overrides, legacy cloud/model credential fields, runtime endpoint and tuning settings, and custom model directory settings. Clear auxiliary model pins, such as the compaction summarizer model and legacy watchdog model, without resetting the surrounding non-model feature preferences.

Preserve configuration outside that boundary, including agent lifecycle and port settings, execution preferences, non-model watchdog controls, history/retention preferences, tool permissions and UI preferences. Preserve unrelated configuration keys; if a malformed or unsupported configuration cannot be safely transformed, report the problem rather than falling back to a destructive replacement. The implementation plan must enumerate the exact fields from the current schema and test the clear/preserve boundary.

Reset is allowed to retain the config file because non-setup settings live there. It must still reliably trigger fresh setup. Reuse the existing wizard persistence/lifecycle for this purpose rather than relying solely on config-file absence. The command and CLI must share the relevant path/schema contract instead of maintaining divergent copies of it.

### Credentials

Clear credentials owned by Cercano, including API keys and persisted OAuth access/refresh token sets. Enumerate the entire Cercano credential namespace rather than only the currently configured profile names, so orphaned profile credentials are removed too.

Current code stores these entries under the `cercano` OS-keychain service, keyed by profile name. Both ChatGPT and Anthropic token sources can refresh and persist token sets, so reset must exclude concurrent credential writers. Any other Cercano-owned credential storage found during implementation must be included in the inventory or explicitly reported as a scope blocker; it must not silently survive a claimed complete reset.

Do not read or print secret values just to list or remove entries. Do not create credential backups or write secrets to logs, config, wizard state or a reset journal. Remove legacy credentials embedded in the scoped config fields as well.

Do not delete shared AWS credentials, shell environment variables, browser sessions, another application's login files, or external runtime credentials. Do not perform remote token revocation or provider API calls. External authentication may make a subsequent sign-in easier, but it is outside a Cercano setup reset and must not be represented as cleared.

### Wizard state

Remove all previous answers, selected profiles/models and rollback baseline. Replace stale or malformed resume state with a fresh initial run only as part of the confirmed reset. Respect the existing wizard state path rules, including `CERCANO_WIZARD_STATE` and `XDG_CONFIG_HOME`.

### Preserved files and stores

Never recursively delete a Cercano data/config directory as the reset operation. Leave model bytes untouched in default directories, custom directories and external runtime-managed stores. Preserve download metadata needed to discover or reuse those files; resetting an active model choice is not permission to delete its download record.

Do not delete runtime installations, conversation/history databases, attachments, usage history, projects, efforts, logs, themes, permission records, or unrelated preferences. Do not uninstall or reconfigure externally managed runtimes. Stopping Cercano-owned runtime processes during a safe agent shutdown is not deletion of their installations or downloaded models.

## Coordination, failure and recovery

Reset must operate with exclusive access to the setup state. It must not reset settings or credentials underneath active conversations, downloads, credential refreshes or settings writers.

After confirmation, an idle/background Cercano agent may be shut down using the existing graceful lifecycle. Wait for actual shutdown rather than treating the shutdown acknowledgement as proof that writers have stopped. Attached clients may automatically spawn a replacement agent; prevent that race during reset or refuse before mutation with an actionable instruction to close clients. Do not kill unrelated processes, force-kill active work, or silently interrupt another client's conversation to proceed.

A timeout, unknown process state, inability to secure exclusive access, or inability to establish credential-store access must not be treated as successful completion. Before irreversible changes, validate paths, config transformation and prerequisites as far as possible.

The OS keychain and filesystem do not provide a single transaction. Failures after partial progress must therefore be reported accurately with a nonzero result and actionable retry guidance. The command must be safe to retry: missing entries/files are already reset, surviving credentials can still be enumerated after profiles are cleared, and partial failure must never restore deleted credentials or erase preserved data. Do not claim full rollback or all-or-nothing behavior across those stores unless the implementation can actually provide it.

An inaccessible keychain is different from an accessible, empty credential store. Do not silently skip credential failures. A fresh installation or repeated reset with no stored credentials must succeed when the relevant stores are accessible.

## Decisions

### Setup-only versus full user-state reset

The user selected setup-only reset and explicitly excluded history/conversation deletion.

| Axis | Setup-only reset — chosen | Full user-state reset |
| --- | --- | --- |
| Clear boundary | Setup settings, routing, credentials and wizard state | Those plus history and non-setup user state |
| Preservation | Conversations, downloads, installations and unrelated preferences | Downloads and installations only |
| Fit | Tests setup without losing work | Tests a broader fresh-installation experience |

### Terminal interface

The user accepted `cercano reset --setup` as a terminal-only command.

| Axis | Terminal-only — chosen | Terminal plus interactive command |
| --- | --- | --- |
| Entry points | One explicit confirmation flow | Terminal and live-UI flows |
| Coordination | Can establish safe offline/exclusive reset before modification | Must additionally terminate or transition an active UI session |
| Scope | Sufficient for setup testing | Additional convenience not needed in this effort |

### Download location and rediscovery

Following the user's question about normalized download locations, the agreed continuation restores default model paths while preserving files everywhere. Both llama-server and mistral.rs default to `~/.cercano/models`; custom directories are optional overrides, not a reason to retain old setup configuration.

| Axis | Restore default paths — chosen | Preserve custom paths |
| --- | --- | --- |
| Setup fidelity | Clears previous model-directory settings | Retains a setup exception |
| Normal downloads | Remain discoverable in the default location | Remain discoverable in the default location |
| Custom files | Preserved; directory may need re-adding | Preserved and still discoverable |

Preserving non-setup settings rules out deleting the complete config file. Reusing the existing fresh wizard state is the intended way to reopen onboarding with a retained config; a second competing onboarding-completion state is not needed.

## Acceptance criteria

A confirmed reset of a configured fixture clears the complete audited setup-field set and all Cercano-owned API-key/OAuth entries, including orphaned entries, while preserving conversation/history data, unrelated preferences, installed runtimes and model-file contents.

A subsequent ordinary CLI launch enters the first wizard step despite the preserved config file. No pre-reset answers or rollback baseline survive. Default-location models can be discovered and reused without re-downloading, and files at custom paths remain unchanged even when those paths are no longer configured.

Declined confirmation, invalid arguments, blocked coordination, inaccessible credential storage and malformed config produce truthful outcomes without deleting preserved state. Repeated reset succeeds; injected partial failures are reported and safely recoverable by retry. Concurrent agent launch, token refresh and settings-writing scenarios cannot repopulate old state after a reported successful reset.

Verification includes pure configuration boundary tests, fake credential stores with orphan/failure cases, temporary filesystem preservation fixtures, wizard startup/completion tests, command confirmation/exit tests, and process-coordination integration tests. Use temporary directories and injected stores; do not reset the developer's real credentials or settings as part of automated verification.

## Non-goals and execution limits

This is not a factory reset, uninstall, credential revocation service, conversation cleanup, or mechanism to delete downloaded models. It adds no interactive reset tool and changes no model routing defaults except restoring the current installation defaults through reset.

Implementing the feature does not authorize running the destructive command against the user's real installation. That requires a separate explicit request. Preserve unrelated repository work; do not push without permission.

## Read-only evidence

The standalone CLI in `source/clients/cli/main.go` currently opens setup when explicitly requested, when config is missing, or when `wizard.Load()` finds resume state. Merely retaining the config and deleting wizard state would not trigger fresh setup.

`source/clients/cli/internal/wizard/wizard.go` persists `wizard_state.yaml`, with a first `locus` step and a rollback baseline; its path honors `CERCANO_WIZARD_STATE` and `XDG_CONFIG_HOME`.

`source/server/internal/secrets/secrets.go` exposes List/Delete on the `cercano` keychain namespace without requiring secret reads for enumeration. ChatGPT and Anthropic token sources use that profile-keyed store and can write refreshed tokens back.

`source/server/pkg/agentclient` exposes `ShutdownAgent`, and server shutdown is asynchronous. Client reconnection logic can spawn replacement agents. Exclusive reset cannot be inferred solely from a successful shutdown RPC.

Default model directories and custom overrides are defined in `source/server/pkg/config/config.go`. Both llama-server and mistral.rs download target resolvers use the first configured model directory or `~/.cercano/models`.
