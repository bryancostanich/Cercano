# Cercano

**An AI coding agent designed to use context and compute deliberately.**

Cercano brings frontier reasoning, built-in delegation, and open-weight models
into one terminal workflow. Keep demanding reasoning in the main conversation,
hand bounded work to appropriately priced subagents, and control where each kind
of task runs—on your hardware or with a hosted provider.

## What makes Cercano different

| Feature | What it is | Why it matters |
|---|---|---|
| **[Advanced Context Management](docs/agent/features/context-management.md)** | Rolling background compaction and layered summaries | Keep long conversations manageable without pausing for a summarizer; reduce the history carried into subsequent requests. |
| **[Built-In Delegation](docs/agent/features/built-in-delegation.md)** | Subagents with their own context and scoped tools | Keep routine exploration and mechanical work out of the main context and off expensive models when a cheaper route is sufficient. |
| **[Advanced Metrics](docs/agent/features/advanced-metrics.md)** | Token usage broken down by provider, model, and source | See where tokens go and make informed routing and budget decisions. |
| **[Advanced Routing Engine](docs/agent/features/advanced-routing.md)** | Independent task, quality, and destination settings | Choose the right capability and price point for each kind of work instead of using one model for everything. |
| **[Integrated Local Runtime](docs/agent/features/integrated-local-runtime.md)** | Managed llama-server runtime for open-weight models | Use your hardware without separately assembling the inference stack. |
| **[OpenAI-Compatible Providers](docs/agent/features/openai-providers.md)** | Connections to compatible hosted and self-hosted endpoints | Change providers and models without changing your coding workflow. |
| **[Advanced Terminal UI](docs/agent/features/advanced-terminal-ui.md)** | Rich formatting, responsive layouts, themes, and visible agent activity | Follow complex work without deciphering raw logs. |
| **[Automatic Session Retention](docs/agent/features/automatic-session-retention.md)** | Saved conversations, automatic titles, history, search, and resume | Return to prior work without reconstructing the conversation. |
| **[Client/Server Architecture](docs/agent/features/client-server-architecture.md)** | An independent agent process serving multiple client sessions | Keep session state separate and use the same agent through interactive and headless clients. |

## Get started

### macOS (Apple Silicon) installation

Install via Homebrew (requires macOS 12+; Intel not supported):

```bash
brew install bryancostanich/tap/cercano
```

This installs both the `cercano-cli` terminal client and the `cercano` agent server. Start the terminal client:

```bash
cercano-cli
```

To upgrade:

```bash
brew upgrade cercano
```

Open `/config` to configure models, routing, and permissions. Then try a bounded,
read-only task in a repository you know:

> Find where this project loads its configuration. Delegate the code search to
> a read-only subagent, then explain the result with file references. Do not edit files.

### Development source build

For development work, follow the [source build instructions](docs/agent/self-dev.md#layout--build) and the [agent setup guide](docs/agent/README.md). The server and terminal client are separate Go modules; the development launcher handles building both.

Once the launcher is installed:

```bash
cercano
```

Note: The development launcher `cercano` is separate from the Homebrew-installed `cercano-cli` and `cercano` binaries. See [local signing and notarization](docs/local-macos-signing.md) for contributor information on macOS builds.

**Windows:** there is an experimental, explicitly **unsigned** Windows x64 ZIP built by the same manual release pipeline. It is not a supported release; see [docs/windows-artifact.md](docs/windows-artifact.md).

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
