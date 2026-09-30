# OpenAI-Compatible Providers

## What it is

Cercano can connect to OpenAI-compatible Chat Completions endpoints using a
configured base URL, credentials, and model. That includes compatible hosted
open-weight services and self-hosted inference servers, not just OpenAI itself.
Profiles connect these endpoints to Cercano's routing system.

## Why it matters

Choose inference based on capability, price, and where you want work to run—not
on whether your coding agent has a bespoke integration with a particular vendor.
Change providers or try new open-weight models while keeping the same terminal,
sessions, tools, and delegation workflow.

## Try it

Open `/cloud`. Configure a Chat Completions profile with your provider's base URL,
credentials, and a tool-capable model. Assign the profile to the desired cloud
destination, save the settings, and test a small read-only task.

See the [OpenAI-compatible setup guide](../cloud-openai.md) and
[cloud routing guide](../../cloud-routing.md) for configuration details.

## Controls and limitations

- Compatibility is not universal certification. Providers differ in tool
  calling, streaming, context limits, image support, and usage reporting.
- A model that handles plain chat may still be unsuitable for an agent's tool loop.
- Hosted endpoints receive the content routed to them and have their own
  pricing and data policies. An open-weight model does not imply local inference.
- OpenAI Responses and subscription sign-in are distinct provider paths; support
  for Chat Completions does not imply identical behavior across those paths.

Back to the [feature index](README.md).
