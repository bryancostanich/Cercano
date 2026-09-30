# Advanced Routing Engine

## What it is

Cercano separates three choices: **what kind of task** is being performed,
**how much model capability** it needs, and **where it runs**. Task classes such
as reconnaissance, mechanical development, investigation, and implementation
have saved quality and destination settings.

Primary, Secondary, and Local destinations are independent of model quality.
Cloud destinations have their own model profiles and optional backups;
redirects and placement policy control the final destination.

## Why it matters

Do not pay one model's price for every kind of work. Route routine exploration
to an economical model and reserve stronger models for difficult reasoning.
Run suitable work on your own hardware or an open-weight provider without
assuming that inexpensive always means local—or that local always means small.

## Try it

Open `/config`, select **Routing**, and inspect the assignments for
**Reconnaissance** and **Implementation**. Adjust their destination and quality
independently, then save the routing changes. Run a delegated task and inspect
the reported route to confirm the resulting placement.

## Controls and limitations

- Quality selects a model within a destination; it does not grant tools or
  override placement restrictions.
- Secondary is a separate destination, not simply Primary's backup.
- Redirects change initial placement; backups respond to failures. Those are
  different mechanisms, and not every route has an implicit fallback.
- A route configured as Local can be redirected. Check the final destination
  rather than inferring privacy or cost from a task's default setting.
- Model quality and savings are workload-dependent, not guaranteed by a tier name.

See the [routing reference](../../cloud-routing.md) for defaults, model bindings,
redirects, and failure behavior, and [placement policy](../locus-mode.md).

Back to the [feature index](README.md).
