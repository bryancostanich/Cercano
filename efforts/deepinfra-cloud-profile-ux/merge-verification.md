# Integration with main's authentication, prompting, and runtime-context changes

## Preserved work and merge base

- Integration started on `feat/cloud-profile-routing` from `77f0ae824224`, merging `main` at `8e253219`.
- Common base: `35fc2d8ae95e8a0c39d28d75a403a5a588aec568b`.
- Main's three modified planning documents and untracked root `go.mod` were preserved in stash `5574cc31cc1e80e76b09084f6685d2b0950539c5`, named `pre-cloud-profile-routing-merge: preserve main WIP at 8e253219`.
- Eight leftover feature test edits were proved byte-identical to gofmt-normalized HEAD versions and discarded as formatting-only noise. Their reviewed diff was retained in `/tmp/cercano-merge-leftover-tests.diff` during execution.
- Thirteen conflict paths were resolved directly. Generated protobuf code was regenerated, not hand-merged. No source/stash deletion or push was performed.

## Integrated contracts

1. **Authentication recovery:** main's request-owned login/fallback/cancel state machine remains authoritative. Both independent destination chains use it. Subscription credentials use the shared host credential owner; construction of unused subscription backups stays lazy. Backup labels name the configured profile, not just its transport.
2. **DeepInfra/static credentials:** a canonical DeepInfra API URL is no longer mistaken for an anonymously authenticating proxy. Missing API keys are typed non-interactive authentication failures. Store failures retain profile identity, remain terminal credential failures, and redact their cause from user-facing errors. Custom proxy endpoints can still intentionally have no key. Configured backup credential errors remain available for reporting when selected rather than disappearing during construction.
3. **Destination and model isolation:** recovery re-resolves the backup's own quality/image choice. Request-scoped authentication fallback also selects the correct budget/profile metadata on later calls; a fresh request has no inherited fallback permission. Secondary retains no automatic cross-destination fallback.
4. **Image safety:** every ordinary or authentication-selected backup path checks model/image availability. A transport's default text-model vision flag cannot incorrectly veto a separately confirmed image model, or authorize an unconfirmed one.
5. **Live-call continuation and accounting:** authentication recovery does not replay the whole tool loop. The outer permitted Local authentication fallback carries actual model/context attribution. Managed Local capacity and instance identity are prepared again when later iterations run on that selected runtime.
6. **Runtime context:** main's observed-capacity checks and pointer-valued context configuration are preserved. Routing/usage wrappers forward runtime confirmation. PreparedTarget uses the saved destination rather than the old binary Primary/Open selector.
7. **Profile edits during login:** profiles now contain sparse maps, so comparisons use value equality and login snapshots/results are cloned. Quality changes invalidate a pending login; reauthentication preserves choices, image model, AWS metadata, and active status. Initial login can activate its profile but does not invent or swap an independent backup.
8. **Prompting and UI:** main's embedded protocol prompts, request-owned confirmation/reconnect behavior, and conversation-search work remain in the merge. Local-only query thinking controls remain local-only; DeepInfra does not receive llama-server template parameters.

## Protobuf collision

Both branches had allocated LLMChatRequest field 9. Main's `disable_thinking = 9` is preserved. `fallback_tier = 10` remains, and the unlanded feature's `tier` moves to field 11. A protobuf round-trip/descriptor regression checks all three independently. Agent and workers must be rebuilt together; compatibility with the previous unlanded feature worker binary is not claimed.

## Regression coverage added/adapted

- Named login, cancel, and fallback across Primary and Secondary, streaming and non-streaming, with a DeepInfra backup.
- Original override/fallback quality retained, correct profile/context after explicit fallback, and no authorization leak to a fresh request.
- API-key rejection does not launch subscription login.
- DeepInfra missing-key/store failures are typed and sanitized; worker store failures cause zero anonymous or backup HTTP requests.
- Both destination chains retain lazy subscription construction and the shared credential owner.
- Profile choice maps survive reauthentication without aliasing; edits cancel obsolete login attempts.
- PreparedTarget selects Secondary correctly, and task wrappers preserve observed runtime capacity/instance identity.
- Thinking and routing fields survive worker protobuf transport without field-number collision.

## Final pre-landing checks

Server build and vet passed. The following race-enabled gate passed all 28 packages:

```sh
go test -race -count=1 ./pkg/config ./pkg/agentclient ./internal/agent ./internal/dispatch ./internal/inference/... ./internal/hostsvc/config ./internal/hostsvc/providers ./internal/hostsvc/credentials ./internal/hostsvc/tools ./internal/hostsvc/persistence ./internal/llm/... ./internal/anthropicauth ./internal/chatgptauth ./internal/worker ./internal/server ./internal/runner ./internal/usage ./internal/toolstack ./internal/protocols ./internal/capabilities/builtins
```

CLI `go build ./...`, vet, and `go test -race ./internal/ui ./internal/slash ./internal/wizard -count=1` passed (UI 5.113s, slash 1.929s, wizard 1.657s).

No live OAuth, paid inference, restart, or launch was performed. These checks use fixtures and local worker/HTTP/gRPC processes. The pre-merge main stash is intentionally retained rather than automatically applying superseded planning edits over the landed result.
