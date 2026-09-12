# Setup-reset implementation inventory

Branch: feat/new-user-setup-reset, based on main d9d21fd3. Worktree: /Users/bryancostanich/git_repos/bryan_costanich/Cercano-new-user-reset. Original planning files and unrelated token-accounting work in Cercano are preserved. No real credential/settings operations performed. The initial broad worktree delegation failed validation; a separate actual scoped git_worktree call succeeded, followed by copying only the approved effort documents.

## Credentials

`internal/secrets/secrets.go` is the only production keyring.Open call found under source. It opens service `cercano`, keyed by profile. List calls Keyring.Keys without reading secret values; Delete calls Remove. The adapter currently normalizes missing errors on Get but not Delete, so reset needs idempotent missing-entry handling without masking inaccessible-store errors.

ChatGPT and Anthropic source.go serialize OAuth token sets into this same Store, not separate user files. `internal/hostsvc/credentials` wraps the store, serializes mutations and rejects stale refresh commits per generation; that protection is in-process, not sufficient for a separate reset command. Server cloud login handlers commit into it. Clear every enumerated entry, not only configured profile names.

`cmd/cercano/main.go` migrates legacy CloudAPIKey into the keychain and then saves the config. Reset must intercept command dispatch before this path; otherwise a retired plaintext key could be persisted again.

`internal/cloudfactory/factory.go` delegates Bedrock authentication to AWS SDK configuration. Shared AWS state, external provider/browser sessions, shell variables and externally managed runtime authentication are not Cercano-owned reset targets. No extra Cercano credential files were found in the production Go source audit of keyring, credentials/token JSON/YAML paths, auth packages and host credential implementation. This is source evidence, not inspection of real keychain contents.

## Configuration

Root reset keys: ollama_url, open_runtime, open_model, embedding_model, cloud_provider, cloud_model, cloud_api_key, cloud_base_url, cloud_profiles, active_cloud_profile, backup_cloud_profile, secondary_cloud_profile, secondary_backup_cloud_profile, task_assignments, secondary_redirect, local_redirect, locus_mode, llama_server, mistralrs, models, model_profiles.

Nested reset keys: compaction.summarizer_model; watchdog.model. Preserve their sibling preferences. Preserve agent settings, execution mode, port, worker idle timeout, ToolLoop, history retention, permissions and unknown unrelated YAML fields. The implementation must restore current Defaults, not zero out required runtime/locus defaults. config.Load performs migrations; verify reset output cannot recreate legacy profiles.

The existing config.Save serializes a typed Config and is unsuitable for preserving unknown YAML keys during this reset. Use an explicitly scoped yaml.Node transformation and reject ambiguous aliases/merges rather than silently broadening scope.

## Writer/launch inventory

- `cmd/cercano/main.go`: normal/background/MCP startup reads config, opens credentials, migrates legacy tokens; direct config-writing commands also call config.Save.
- `cmd/agent/main.go`: alternate legacy server entry point loads config and can expose configuration services. Must participate even if not the recommended executable.
- `internal/hostsvc/config/config.go:Persist`: writes the whole cached current configuration; an old live agent can repopulate reset values.
- Host credential service and login/refresh paths can persist tokens after a separate process begins reset.
- CLI `internal/ui/wizard_page.go` writes wizard state directly, and sends profile/settings mutations to the agent. Hold a participation lease before wizard state/config reads.
- `pkg/agentclient`: autolaunch and reconnect may spawn a fresh process. Existing launch_lock_unix.go guards only startup's short critical section; launch_lock_other.go is a no-op outside Unix. Neither is a reset-safe lifetime lease.
- `internal/server/shutdown.go` closes the listener before draining finishes. Do not equate failed connection attempts with absent state writers.

Shared coordination must cover all participating startup/writer paths. Existing binaries cannot honor a new lock; detection/refusal of known nonparticipating processes and clear compatibility limits are mandatory. No claim that a lock can constrain arbitrary externally started old binaries or unrelated scripts should be made.

## Preserved state and wizard

The wizard's typed persisted State includes a rollback Baseline. Clearing only config cannot remove that old baseline; clearing only wizard state while preserving config does not reopen onboarding. Share the existing wizard state/path schema between command and CLI, then write a genuinely fresh initial run.

The default model locations for llama-server and mistral.rs are ~/.cercano/models; explicit custom ModelDirs are optional. Download discovery records are distinct from chosen tier/model settings (for example mistralrs/catalog_records.go records concrete catalog payload paths). Do not delete these records, model bytes, runtime installations or external Ollama stores.

Conversation databases, project/effort files, histories, usage data, attachments, logs and preferences are outside the reset target set. Tests must use sentinel fixtures and injected stores, never inspect or reset the real installation.
