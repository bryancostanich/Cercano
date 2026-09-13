# Reset setup for developer testing

```sh
cercano reset --setup
```

This is a **developer reset**, not a factory reset or a coordinated maintenance operation. Run it from a terminal and type `RESET` at the confirmation prompt. There is no interactive `/reset` tool and no unattended `--yes` flag.

**Other sessions may stay open.** The command does not stop, drain or lock them. You control their activity. In-flight work may error, and another session or token refresh may write old settings/credentials back afterward. If that happens, pause the relevant work and rerun the command.

## What resets

- Model choices and overrides, local runtime settings/endpoints/tuning, custom model paths and cloud profile model/image choices.
- Cloud profiles, Primary/Secondary bindings and backups, task routing overrides, redirects and locus choice.
- Cercano-owned stored API keys and OAuth token sets, including entries left over from deleted profiles, plus legacy inline credential fields.
- Previous setup answers and rollback baseline. The next normal interactive client launch begins a fresh setup run.

Configuration returns to the current installation defaults for those fields—not arbitrary empty values. Configuration outside that boundary stays intact.

## What stays

- Conversations and history, usage data, attachments and project/effort files.
- Non-setup preferences, permissions and surrounding non-model feature settings.
- Downloaded model files, download-discovery metadata and installed runtime binaries.
- Credentials owned by other applications, shared AWS configuration, environment variables and browser/provider sessions. Tokens are removed from Cercano's store, not remotely revoked.

Both llama-server and mistral.rs normally download under `~/.cercano/models`; those files remain discoverable. Custom model directory settings return to defaults, but their files are not deleted. Re-add custom paths if needed.

## Running agent and setup

When an agent is listening at the configured loopback port, the command asks it to reset and refresh its live configuration/provider routing. Existing client connections and conversation stores remain intact. Otherwise, the command performs the same scoped reset locally; it does not start an agent just to reset it.

After success, start your usual interactive client, `cercano-cli`, to go through fresh setup. Existing sessions can continue or reconnect normally as you manage their lifecycle. Reset does not automatically turn every open UI into a wizard or guarantee that work already in flight changes models mid-request.

The running agent must include the new reset RPC. If it is an older build, the command reports that it needs updating/restarting; it does not silently reset the disk behind a reachable old agent. Sessions can reconnect after that ordinary agent update.

## Errors and retries

The keychain, live settings, config file and wizard file are separate stores, not a single transaction. Errors report completed steps when known; rerunning is supported. If a connection fails after an agent request, its result may be unknown—the command says so and does not attempt a second local reset.

Malformed/ambiguous config, inaccessible keychain, invalid file targets or a failed wizard write are reported rather than presented as success. Config and wizard paths cannot refer to the same file. No full application directory is deleted and no backup containing secret values is created.

Exit status is `0` for success/help, `1` for cancellation, unavailable terminal or execution failure, and `2` for invalid arguments. Help, missing scope, declined confirmation and noninteractive input do not trigger reset.
