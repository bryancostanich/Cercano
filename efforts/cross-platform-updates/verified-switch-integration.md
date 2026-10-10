# Verified images at selection switch — native acceptance

Native run https://github.com/cercano-ai/Cercano/actions/runs/38028612163
Source fe50342c8be68b3b17d90f5a9c484a39d7a398aa. Windows/Linux/macOS pass.

PreflightFile/StageFile now retain the compressed snapshot SHA-256 with the member
manifest. The transaction requires explicit trusted image/namespace policy,
compares that archive digest with journal intent, derives the version path from
the journal and verifies both executable members. Checks run before recording
intent, before publication, before acknowledgement and on selected reentry.
Changed images after publication leave switch-intent and report the published
state rather than claiming acknowledgement or health. A reproduced symlinked
version-ancestor escape is refused before journal writes.

Tests exercise real archive extraction through selection publication as well as
inert fixture headers, wrong-platform/missing/tampered images, artifact mismatch,
post-intent/post-publication changes and subprocess recovery. No inspected image
is executed. Format/content checks do not establish platform signing, embedded
version identity or runtime health. Trusted manifest and installation-policy
resolution remain responsibilities of the acquisition/application integration.

Next recovery work needs the complete prior selection snapshot in SQLite; version,
generation and artifact hash alone cannot reconstruct a prior staged directory
once the selection file has been replaced. This remains within the approved
SQLite extension direction, not a proposed second journal.

First-activation recovery requires a separate decision: when prior selection was
explicitly absent and candidate health fails, either remove only the verified
updater-created selection to restore absence, or retain the failed pointer and
require manual repair. Restoring absence is recommended with exclusion, exact
content/identity checks and owned-candidate process completion proof. Because it
introduces a selection-file deletion boundary, that behavior and its journal
transition are pending user approval. No deletion/recovery implementation was
added; current prior-none rollback/restored records remain invalid.

No live selection, installed binary, real agent or user data changed. Full updater
execution, health recovery, package backends and UI remain incomplete.
