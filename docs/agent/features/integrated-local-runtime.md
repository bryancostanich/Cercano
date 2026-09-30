# Integrated Local Runtime

## What it is

Cercano integrates a managed **llama-server** runtime for open-weight models.
Runtime setup, model selection, and process lifecycle are part of the agent
experience, rather than a separate inference service you must assemble yourself.
Ollama is also supported; the selected backend determines how local models run.

## Why it matters

Put your own hardware to work without making inference infrastructure another
project. A local runtime provides a practical destination for routine delegation
and other suitable tasks, avoiding provider per-token charges for that inference.

## Try it

After following the [build and setup guide](../README.md), run:

```bash
cercano setup
cercano
```

Open `/config` and inspect **Runtime** and **Local Models**. Select the managed
runtime and a compatible model appropriate for your machine, allow required
downloads to finish, then assign a suitable task to Local in **Routing**.

## Controls and limitations

- Runtime integration does not eliminate model downloads, disk requirements,
  or CPU/GPU and memory constraints. Larger models need more resources.
- Runtime preparation and the active backend are separate choices; do not
  assume installation alone switches every route to llama-server.
- Model support for tools and its ability to follow instructions matter for
  agent work. Quality and speed vary by model, quantization, and hardware.
- Local inference avoids hosted inference fees, not hardware or electricity
  costs. Tools, downloads, or other routes may still access the network.

See [embedded inference](../../features/embedded_inference/README.md) and
[task routing](advanced-routing.md).

Back to the [feature index](README.md).
