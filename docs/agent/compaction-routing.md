# Compaction routing and execution policy

Compaction is a first-class task in Settings → Task routing. Its default is **Secondary / Economy**. The same assignment serves main-conversation background compaction, manual regeneration, and inline sub-agent compaction.

The equivalent optional configuration is:

```yaml
task_assignments:
  compaction:
    destination: secondary
    quality: economy
```

Omitting this entry uses the same default. It does not inherit Chat or Dispatch quality. Model selection follows the selected profile's quality overrides and vendor recommendations. The host reads the live routing graph; workers use the existing host-supplied routing snapshot. Existing settings serialization and worker transport carry the new task without a separate schema or migration.

## Availability and fallback

Compaction uses the ordinary destination resolver, redirects, locality restrictions and profile backup chain. Secondary is attempted directly; there is no hidden local-first attempt, and no compaction-specific fallback to Primary. A configured Secondary backup still works at the requested quality. If Secondary is unavailable without a usable backup or redirect, compaction fails nonfatally rather than choosing an unauthorized destination. Configure Secondary or explicitly choose another Compaction route on installations without it.

An explicit Local assignment uses local capacity-aware chunking. Local failures do not silently spend cloud tokens. Primary retains the normal Primary locality/availability policy. Cloud-only and local-only restrictions remain enforced.

The legacy `compaction.summarizer_model` is retained without destructive configuration migration. It overrides the local model only when the task explicitly selects Local and the resolved provider is local. It does not override Secondary, Primary, or a redirected cloud model. The new quality setting otherwise uses the same model resolution as other tasks.

## One execution budget

A complete compaction pass has a shared **six-minute maximum**, including its segment summaries, consolidation and provider failover. Inline, scheduled and manual entry points use this policy. A stricter caller deadline still wins; cancellation and generator shutdown remain effective. There is no detached two-minute fallback clock and no separate 90-second inline timeout.

Background scheduling/debouncing and inline scheduling remain different. Compaction algorithms, retention thresholds, token-budget accounting, and dispatch evidence recording are otherwise unchanged. This does not change the cumulative dispatch token cap or fix its estimated-compaction accounting.

## Verification

Deterministic tests cover the default and saved assignments, routing UI editing, worker configuration round trips, actual worker-built compaction, cloud backup quality, local-only/cloud-only restrictions, unavailable routes, live host graph reads, retained summarizer evidence, and shared deadlines/cancellation. No live model requests are needed.
