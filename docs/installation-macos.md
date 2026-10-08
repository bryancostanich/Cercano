# Installing Cercano on macOS

Cercano is distributed for **Apple Silicon Macs running macOS 12 (Monterey) or
later**. Intel Macs are not supported by this release.

> Publication to the dedicated tap is in progress: the signed v0.20.3 release
> and the tested formula are ready, but the tap contents and the release
> pipeline's publishing credential are still being staged. These instructions
> will work once the tap correction is published.

## Install

```bash
brew install cercano-ai/cercano/cercano
```

This installs two binaries:

| Binary | Role |
|---|---|
| `cercano` | the agent, which does the work |
| `cercano-cli` | the terminal client you interact with |

Start the terminal client:

```bash
cercano-cli
```

The client launches the agent automatically the first time you use it. You do
not need to start `cercano` yourself, and nothing is installed as a background
service — no launch agent, no daemon, nothing that runs at login. The agent
runs only when you use Cercano.

## First run

Cercano ships **no models and no credentials**. The binaries are self-contained,
but on first use you will need to configure at least one of:

- **A cloud provider** — an API key for your chosen provider.
- **A local model** — downloaded on demand, which needs disk space and time.

Run `cercano-cli` and follow the setup prompts. Configuration and conversation
history live in `~/.config/cercano/`, outside the Homebrew installation.

## Upgrading

```bash
brew upgrade cercano
```

**A successful upgrade restarts a running agent for you.** The agent finishes
its in-flight work, shuts down cleanly, and the new version starts in its
place; connected clients reconnect. This is what makes the upgrade actually
take effect rather than leaving the old version running.

Specifically:

- If an agent is running, it is drained and replaced.
- If no agent is running, **nothing is started**. Upgrading never launches an
  agent you did not ask for.
- If the download or installation fails, your running agent is left alone.

If the post-install restart reports a failure, the new files are already
installed — only the restart did not complete. Retry it:

```bash
cercano restart-after-upgrade
```

Or restart from an attached client with `/restart-agent`.

### If you run the agent on a custom port

If you set the port through an environment variable rather than the config
file, Homebrew will not inherit it, and the restart cannot find your agent.
Pass the address explicitly:

```bash
cercano restart-after-upgrade --address 127.0.0.1:50053
```

## PATH conflicts with a development checkout

If you also run Cercano from a source checkout, you may have a development
launcher earlier in your `PATH` than Homebrew's. Check which one you are
running:

```bash
which -a cercano cercano-cli
```

Homebrew installs to `/opt/homebrew/bin`. If a development path appears first,
that copy wins, and `brew upgrade` will not change which binary you run.

This matters for upgrades too: the restart only acts on an agent belonging to
the **same Homebrew installation**. A development agent is deliberately left
alone rather than being restarted with Homebrew binaries.

## Troubleshooting

**"cercano" cannot be opened because the developer cannot be verified.**
Released binaries are signed with a Developer ID and notarized by Apple, so
this should not happen. If it does, you likely have an unsigned build from
another source. Reinstall with `brew reinstall cercano`.

**The client cannot reach the agent.** Confirm nothing else is bound to the
configured port, then start a fresh agent by running `cercano-cli` again.

**The upgrade restart failed.** See the retry commands above. Your data is not
affected — configuration and conversations are stored outside the installation.

**Checking versions.** Both binaries report their own version offline:

```bash
cercano --version
cercano-cli --version
```

A client and agent from different versions can still talk to each other;
Cercano does not block on a version mismatch. Restarting the agent after an
upgrade is still worthwhile so you actually get the new version.

## Uninstalling

```bash
brew uninstall cercano
```

**Your data is deliberately kept.** Uninstalling removes the binaries but
leaves `~/.config/cercano/` intact, including your configuration and
conversation history, so reinstalling resumes where you left off.

To remove everything, delete that directory yourself:

```bash
rm -rf ~/.config/cercano
```

Any locally downloaded models are stored separately and are also left in place.
