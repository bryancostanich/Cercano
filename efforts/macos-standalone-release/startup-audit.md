# Installed startup audit

## Verified code path

`source/clients/cli/main.go:84` calls `agentclient.Dial`. The shared implementation is in `source/server/pkg/agentclient/client.go`, not under the CLI module.

Direct inspection of `client.go` confirms:

- `findCercanoBinary` obtains `os.Executable()`, checks for the platform-specific `cercano` filename in its directory using `os.Stat`, and otherwise uses `exec.LookPath("cercano")`. Missing binaries produce an explicit error mentioning both locations.
- There is no explicit `filepath.EvalSymlinks` in that function. An isolated subprocess test on the macOS development host now passes when launching a copied test executable through a Homebrew-style prefix symlink, with a colocated agent fixture and PATH restricted to an empty directory. This verifies the discovery function under that layout, not a complete installed release.
- `autoLaunchServer` executes the discovered binary with argument `agent`, redirects output to `filepath.Join(os.TempDir(), "cercano-server.log")`, adds `CERCANO_AUTOLAUNCHED=1`, and uses `detachSysProcAttr`. It releases the process handle after starting it.
- `ensureServerLaunched` takes an auto-launch lock and retries the connection before spawning.

## Unknowns and next tests

Subsequent entrypoint inspection establishes that bare `cercano` runs the agent rather than launching `cercano-cli`; see update-audit.md. Installed filesystem assets and actual server lifecycle behavior remain unverified. Comments describe an auto-launched agent exiting after its last client disconnects; this audit does not independently establish that behavior.

`source/server/pkg/agentclient/binary_discovery_test.go` now covers Homebrew-style symlinks, PATH fallback with no sibling agent, and a descriptive missing-binary error. It launches a copied test executable outside the checkout and calls the real discovery function; agent fixtures are never executed. A 15-second subprocess timeout and filtered PATH isolate the probe. No production lookup changes were needed.

Verification: from source/server, `go test ./pkg/agentclient -run TestBinaryDiscovery -count=1 -v` passed all three scenarios. The helper skips during the parent invocation and runs inside each marked subprocess. Complete installation/agent-startup integration tests still need isolated configuration, sockets/ports, logs, and processes; no real agent was launched by this test.

Do not use clipboard `pngpaste` lookup or development-repository resolution as evidence for production binary discovery; those were unrelated matches from the initial delegated search.
