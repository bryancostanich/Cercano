# Execution amendment — user-approved live debug reset

The user explicitly authorized resetting while sessions remain open and rejected the offline-only restrictions. Remove the Phase 4 lock implementation rather than expanding it. Phases 4–6 now mean: remove lifetime/process gates; implement a small scoped reset core; invoke it through the running agent with live config/provider refresh, or locally when no agent is running; expose terminal confirmation and fresh wizard state. Do not close sessions, require quiescence, or add legacy-process detection. In-flight work/writeback races are accepted and must be warned about.

Historical Phase 4 tasks below are superseded, not remaining safety blockers. No real-installation reset, push, model deletion or history deletion is authorized. Verification remains through temporary state and fake credentials.

## Live debug reset amendment tasks

- [x] Remove the abandoned offline lifetime/process gates while retaining selective config and shared wizard work.
- [x] Implement and test a small scoped reset core with fake credentials, atomic config publication, fresh wizard state and honest partial failures.
- [x] Add confirmed agent-side debug reset and refresh live configuration/provider state without closing sessions.
- [x] Add terminal `reset --setup`, confirmation and warnings, using the live agent when present and local reset otherwise.
- [x] Verify open-session RPC continuity, fresh setup, credential clearing, preservation boundaries, both builds and affected tests; document limitations and checkpoint.
  The superseded offline design is archived in `offline-design-history.md`; its tasks are not completion requirements for the approved live debug reset. Earlier completed config/wizard work and test results are recorded in verification.md.
