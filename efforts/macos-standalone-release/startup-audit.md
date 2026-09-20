# Installed startup audit

## Verified code path

`source/clients/cli/main.go:84` calls `agentclient.Dial`. The shared implementation is in `source/server/pkg/agentclient/client.go`, not under the CLI module.

Direct inspection of `client.go` confirms:

- `findCercanoBinary` obtains `os.Executable()`, checks for the platform-specific `cercano` filename in its directory using `os.Stat`, and otherwise uses `exec.LookPath("cercano")`. Missing binaries produce an explicit error mentioning both locations.
- There is no explicit `filepath.EvalSymlinks` in that function. Behavior through actual macOS Homebrew symlinks remains a test requirement, not a verified guarantee. Co-installing both executable links is consistent with the lookup strategy.
- `autoLaunchServer` executes the discovered binary with argument `agent`, redirects output to `filepath.Join(os.TempDir(), "cercano-server.log")`, adds `CERCANO_AUTOLAUNCHED=1`, and uses `detachSysProcAttr`. It releases the process handle after starting it.
- `ensureServerLaunched` takes an auto-launch lock and retries the connection before spawning.

## Unknowns and next tests

The reverse path (`cercano` launching `cercano-cli`), installed filesystem assets, Homebrew-style symlink behavior on macOS, and actual server lifecycle implementation have not yet been verified. Comments describe an auto-launched agent exiting after its last client disconnects; this audit does not independently establish that behavior.

Installation integration tests must run outside a source checkout, use a temporary prefix with both binary symlinks, and isolate configuration, sockets/ports, logs, and processes from the active development agent. No launch test was run during this audit.

Do not use clipboard `pngpaste` lookup or development-repository resolution as evidence for production binary discovery; those were unrelated matches from the initial delegated search.
