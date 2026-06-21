# Track cli Context

- [Specification](./spec.md)
- [Implementation Plan](./plan.md) — pending

## Summary

Stand-alone Cercano CLI: Go-based terminal agent harness with 80s cracker/hacker chrome. Talks to the existing Cercano agent over gRPC. Significantly enriches the agent surface (conversation persistence, MCP host runtime, context-window accounting, built-in CLI/shell tool suite). Clean separation between CLI shell and shared agent code — same agent powers VS Code, Zed, and future clients.
