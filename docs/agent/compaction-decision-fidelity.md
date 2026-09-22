# Decision fidelity during compaction

The shared production compaction prompt distinguishes active instructions,
user-approved decisions, pending proposals, verified results, attempted or failed
actions, uncertainty, rejection, and supersession. This applies to main-thread
and dispatch summarization through the shared summarizer wiring.

## Summary contract

The existing `GOAL / DECISIONS / PROPOSALS / FILES / OPEN / STATE` format is
unchanged. Status qualifiers live inside its existing string fields:

- `DECISIONS`: `[instruction]` constraints and `[approved]` decisions, including
  scope, conditions, prohibitions, and stopping rules.
- `PROPOSALS`: `[proposed]` ideas awaiting approval, not rejected alternatives.
- `FILES` and `STATE`: distinguish `[verified]`, `[attempted]`, `[failed]`, and
  `[unverified]` claims. A tool call is not success, an edit is not a passing test,
  and a passing test is not deployment.
- `OPEN`: unresolved questions, ambiguous approval, interruptions, missing
  results, and `[rejected]` or `[superseded]` choices with their replacements.

An assistant's implementation is not evidence of user approval. Summarizers are
instructed not to invent entries, approvals, provenance, or template placeholders.

## Source references

Each rendered transcript message carries a `source sha256:<32 hex digits>`
reference: the first 128 bits of SHA-256 over its rendered evidence, including
role and tool call/result metadata. Tool results now include their call ID and
`is_error` flag, rather than only their textual contents.

References are stable when messages move between chunks. They identify rendered
**content**, not unique database events: identical messages share a reference.
They are not database links, proof of truth, or an authorization mechanism.
References do not include hidden reasoning or image bytes.

The model is instructed to cite source references for important decisions and
results. A prior generated summary is not a new user approval, even though it is
transported in a user-role message. Successive summaries should retain original
references and uncertainty rather than substitute the new summary's reference.
Legacy claims without provenance remain unverified.

## Verification and limitations

`internal/compaction/decision_fidelity_test.go` checks the shared prompt contract,
reference stability and sensitivity to attribution/error status, and three
successive scripted compaction passes through budgeted summarization,
parsing, deduplication, and rendering. Fixtures cover rejection, bounded approval,
correction/supersession, unsupported deployment claims, failed and successful
checks, pending proposals, and interruptions.

These are deterministic pipeline tests, **not live-model semantic evaluations**.
The model can still misclassify facts, omit qualifications, or invent citations;
there is no new output validator or durable decision ledger. Existing chunk
merging and retention policies are unchanged and can still retain conflicting
entries or discard details. The larger prompt and references count toward the
existing summary request budget and may reduce available transcript space.

This change does not repair previously stored summaries. It takes effect for
future compactions after the updated agent is installed and restarted.
