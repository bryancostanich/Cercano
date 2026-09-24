# Release formula and upgrade restart

`cercano.rb.in` is the Apple Silicon release formula template. It is **not ready to publish**: replace `@RELEASE_URL@`, `@VERSION@`, and `@SHA256@` with the final signed/notarized release archive's values. The archive must contain `bin/cercano` and `bin/cercano-cli`; its agent binary must implement `restart-after-upgrade`. Do not attach this hook to the legacy 0.8.1 single-binary formula.

Homebrew runs `post_install` after placing/linking the new keg. The hook invokes the **new keg's absolute executable**, not a PATH lookup. Installation itself only places files. The coordinator does nothing when no owned agent listens at the configured endpoint. It does not register a background service or compare client/agent versions.

For an existing owned agent, the coordinator captures launch settings, validates local restart prerequisites, acquires the existing client auto-launch lock using the agent's original temporary directory, rechecks ownership, requests shutdown through the existing RPC, waits for the actual process to exit, launches the new executable, and holds the lock until the replacement's listener and gRPC connection are ready. The existing agent determines its normal bounded drain/cleanup behavior. No forced-kill fallback is added.

Failures before shutdown leave the existing agent alone. Failures during/after shutdown are reported; they cannot promise that the old agent is still running. An unsuccessful restart does not roll back the installed files. Inspect logs before retrying:

```
/absolute/new/keg/bin/cercano restart-after-upgrade
```

The default endpoint is `127.0.0.1` with the port from Cercano configuration. If the running agent was started with an environment-only `CERCANO_PORT` override not inherited by Homebrew, supply its address explicitly when retrying:

```
/absolute/new/keg/bin/cercano restart-after-upgrade --address 127.0.0.1:50053
```

Automatic discovery of agents on other ports is not implemented. A nil ownership result describes the selected endpoint, not all agents on the machine. Nonstandard launch arguments are refused instead of replayed unsafely.

Run the local formula-method checks with:

```
ruby release/homebrew/test_formula.rb
```

These tests use a small Ruby DSL stub; they do not prove Homebrew's actual lifecycle, sandbox behavior, detached-process survival, or release-archive installation. Those still require the clean-Mac installation/upgrade rehearsal and final formula rendering. Nothing in this directory publishes to the tap.
