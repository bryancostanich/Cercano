# Advanced Metrics

## What it is

The **Token Metrics** settings page shows reported token consumption over time,
with breakdowns by provider, model, and source. It distinguishes known counters
from missing usage and exposes accounting-health warnings, so incomplete data
is not silently presented as measured zero consumption.

## Why it matters

Understand where your token budget goes. Compare the models and work sources
consuming tokens, then use that evidence to refine delegation and routing.
Cost control starts with visibility, not an assumption that every local or
lower-priced route automatically saves money.

## Try it

Open `/config` and select **Token Metrics**. Choose a date range and inspect the
provider, model, and source breakdowns. Check coverage warnings before using
those totals to compare workflows.

Use `/context` for the separate question of how much context the current
conversation occupies; context size is not cumulative billed usage.

## Controls and limitations

- These are **reported usage figures**, not billing-grade accounting. Coverage
  of every inference path is not certified; unknown counters are not zero.
- Cache and reasoning token categories are subsets, not extra tokens to add
  indiscriminately to input/output totals.
- External usage reports can overlap internal attempts. Keep those populations
  separate rather than adding them together.
- The standalone Token Metrics view excludes legacy telemetry records. The
  older `cercano stats` dashboard is not an interchangeable view of these totals.
- Token counts, price-based cost estimates, and estimated savings are different
  quantities. Dollar costs require applicable model prices; savings require an
  explicit comparison baseline. Do not read either as measured invoice savings.

See the [agent guide](../README.md) and
[Advanced Routing Engine](advanced-routing.md).

Back to the [feature index](README.md).
