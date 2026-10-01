# Cercano

**An AI coding agent designed to use context and compute deliberately.**

**Frontier + open, separately or together.** Use a frontier provider through an
available subscription sign-in or API credentials, run open-weight models, or
combine them in one terminal workflow. Keep demanding reasoning in the main
conversation and delegate bounded work to appropriately priced models—on your
hardware or with a hosted provider. Subscription eligibility and limits depend
on the provider; see [provider options](docs/agent/features/openai-providers.md).

Built-in workflows guide research, planning, execution, debugging, and design
decisions. Background context management and a polished terminal interface help
you stay focused as sessions grow, with subagent tabs keeping delegated work
visible without mixing every intermediate step into the main conversation.

## What makes Cercano different

### The right model for the job

**Don't spend frontier compute on every task.** Route demanding reasoning,
routine searches, and mechanical edits independently. Choose quality and
execution destination separately, mixing frontier and open-weight models to
match the work—not forcing every task through the same model.

[Explore the routing engine →](docs/agent/features/advanced-routing.md)

### Delegate the work. Keep the focus.

**Give investigations their own workspace, not your entire context window.**
Built-in subagents work with separate context and scoped tools, then return
focused results. Follow their progress in dedicated tabs while keeping the main
conversation centered on decisions.

Watch a delegated investigation in its own subagent tab:

https://github.com/user-attachments/assets/0656f5b1-a0bd-4faf-a0b3-50db4ea43cbf

[Explore built-in delegation →](docs/agent/features/built-in-delegation.md)

### A process, not just a prompt

**Spend less time telling your agent how to work.** Built-in workflows guide
research, planning, execution, debugging, and design decisions. Reproduce the
failure before fixing it. Compare real alternatives before choosing one. Verify
the result instead of stopping at plausible output.

[Explore agent workflows →](docs/agent/features/powerful-agent-workflows.md)

### Keep working. Let context catch up.

**Long sessions shouldn't mean carrying every old token forward.** Background
compaction turns older conversation into layered summaries while retaining
recent working context. Make room for current evidence without stopping for a
summarizer—with stored history kept separate from lossy summaries.

[Explore context management →](docs/agent/features/context-management.md)

### Know where your tokens go

**Make model choices with numbers, not guesses.** Break token usage down by
provider, model, and source. See what's consuming your budget and use that
visibility to tune your routes; estimated savings stay labeled as estimates.

See the token metrics dashboard in action:

https://github.com/user-attachments/assets/82c65acd-6b7f-4d6e-8a56-dbe0d3133d8c

[Explore token metrics →](docs/agent/features/advanced-metrics.md)

### Local models, less assembly required

**Use your hardware without building an inference stack first.** Cercano's
managed local runtime brings open-weight models into the same workflow as your
cloud providers. Configure a suitable model and let Cercano manage the runtime.

[Explore the integrated runtime →](docs/agent/features/integrated-local-runtime.md)

### Your workflow. Your choice of provider.

**Switch models without switching coding agents.** Connect compatible hosted
services or self-hosted endpoints, and combine them with frontier providers.
Keep your sessions, tools, and delegation workflow while choosing where the
work runs. Subscription sign-in and API access have provider-specific limits.

[Explore provider options →](docs/agent/features/openai-providers.md)

### A terminal that keeps the work readable

**Less log-diving. More clarity.** Rich formatting, subagent tabs, a live context
meter, and responsive layouts organize dense agent activity. Model settings,
routing, and permissions live in the same interface—and themes make it yours.

Take a quick tour of configuration and theming:

https://github.com/user-attachments/assets/89989b0f-91a1-4fa6-aca0-98ce12a446c6

[Explore the terminal UI →](docs/agent/features/advanced-terminal-ui.md)

### Pick up where you left off

**Your last investigation shouldn't disappear when you close the terminal.**
Conversations are saved automatically, with titles, history, search, and resume
helping you find prior work and continue without reconstructing the discussion.

Watch how to resume a saved conversation:

https://github.com/user-attachments/assets/d4a81e65-0206-475c-994b-067174f3a5f2

[Explore session retention →](docs/agent/features/automatic-session-retention.md)

### One agent, more than one way to work

**Use the terminal when you want a conversation. Go headless when you need a
script.** An independent agent process serves multiple client sessions, keeping
session state separate while supporting interactive and automated workflows.

[Explore the client/server architecture →](docs/agent/features/client-server-architecture.md)

## Get started

Standalone release publishing is in progress. For now, follow the
[source build instructions](docs/agent/self-dev.md#layout--build) and the
[agent setup guide](docs/agent/README.md). The server and terminal client are
separate Go modules; the development launcher handles building both.

Once the launcher is installed:

```bash
cercano
```

Open `/config` to configure models, routing, and permissions. Then try a bounded,
read-only task in a repository you know:

> Find where this project loads its configuration. Delegate the code search to
> a read-only subagent, then explain the result with file references. Do not edit files.

**Linux:** there is also an experimental, explicitly **unsigned** Linux x86_64 tarball built natively on `ubuntu-24.04` by the same pipeline. Linux is **not a supported platform yet**; see [docs/linux-artifact.md](docs/linux-artifact.md).

For scripts and automation:

```bash
cercano run "Explain this repository's top-level structure without editing files"
```

Cloud routes require the relevant provider credentials. Local routes require
suitable hardware and model downloads. Model quality, latency, and cost depend
on your configuration; delegation is not a guarantee of savings. See each
feature page for controls and limitations.

## Documentation and support

- [Feature guide](docs/agent/features/README.md): what each differentiator does and why it matters.
- [Agent guide](docs/agent/README.md): setup, commands, permissions, and architecture.
- [Routing guide](docs/cloud-routing.md): task classes, model quality, destinations, and backups.
- [CLI track](docs/features/cli/README.md): implementation status and outstanding work.
- [Developer guide](docs/agent/self-dev.md): building, testing, and working on Cercano.
- [Report a problem](https://github.com/bryancostanich/Cercano/issues): include your version, platform, route/model, and reproduction steps; redact credentials and private content.

## Co-processor mode: deprecated for now

The external co-processor integration—using Cercano as a tool inside another
coding agent—is **deprecated for now**. Its documentation is preserved under
[docs/co-processor](docs/co-processor/README.md). This documentation transition
does not remove runtime functionality or set a removal date.

Native subagent delegation and the standalone agent's support for external
Model Context Protocol (MCP) tools remain separate, current capabilities.
