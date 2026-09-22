# Co-processor mode: deprecated for now

Cercano's external co-processor mode—running Cercano as a tool inside another
coding agent—is **deprecated for now**. New users should start with the
[standalone agent](../agent/README.md).

## What changes

The standalone agent is now the primary product and documentation entry point.
The [previous README](legacy-readme.md) is preserved here, including external
agent setup, the co-processor tool catalog, telemetry hooks, and historical
architecture. Treat it as a historical reference, not current setup guidance.

This is a documentation transition: no runtime functionality is removed by this
change, no removal date is set, and no ongoing support commitment is implied.

## What is not deprecated

- **[Built-in delegation](../agent/features/built-in-delegation.md):** Cercano's
  native agent dispatches tasks to its own subagents.
- **External MCP tools:** the standalone agent can host Model Context Protocol
  (MCP) servers as tool sources. See `/mcp` in the [agent guide](../agent/README.md).
- **[Local inference](../agent/features/integrated-local-runtime.md):** local
  models remain a destination for agent work.

Deprecating an external agent's co-processor integration does not deprecate
these capabilities inside Cercano itself.
