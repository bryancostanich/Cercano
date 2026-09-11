# Automatic model context sizing

## Problem and motivation

Cercano's llama-server configuration currently initializes ContextSize to 8192 and separately tracks whether the YAML key was present through ContextSizeSet. Saving serializes the size but not its provenance. Loading the saved file therefore promotes an implicit fallback into an explicit override. Setup and unrelated settings saves can consequently disable existing model/RAM context profiles.

The defect was reproduced in a temporary file using the real config Load, Save, and Load functions: an absent key loaded as size=8192, explicit=false, serialized as context_size: 8192, and reloaded as explicit=true.

Runtime launch and request budgeting also resolve context independently. A running process can disagree with subsequently edited config, and model-native metadata introduces information unavailable to the existing config/catalog-only budgeting path.

## Goals

Remove the universal 8192 fallback for managed llama-server. Preserve intentional overrides, existing RAM-profile context policy, and model-specific settings. Automatic configuration must remain automatic across setup and unrelated config saves.

Make the runtime the source of truth for context capacity. Resolve a concrete planned size before memory validation and launch, then confirm the serving capacity after startup. Budgeting and context meters must distinguish planned capacity from confirmed capacity and never represent an unknown local window as a known model-family maximum.

Use the existing GGUF metadata reader's ContextLength as the model-native candidate when higher-precedence policy is absent. This is a model capability, not proof that allocating that context fits in RAM. Validate memory before launch rather than launching with an unverified executable default.

## Non-goals

Do not change cloud-provider, Ollama, or mistral.rs default policies. Do not remove intentional 8K embedding profiles or explicit 8192 user overrides. Do not automatically delete existing YAML values: an accidental historical 8K entry is indistinguishable from an intentional one. Do not add a second RAM-profile table, redesign model recommendations, or implement automatic context shrinking to fit memory.

## Constraints and behavior

LlamaServerConfig.ContextSize becomes an optional integer. Absence means automatic; a positive value means an explicit override. Zero and negative explicit values fail validation. Remove the redundant ContextSizeSet flag and its presence parser. YAML saves omit absent overrides. Setup does not fill absence with a generic number. Config snapshots must preserve existing isolation guarantees despite introducing a pointer field.

Resolve planned context in this order: explicit config override, applicable model/RAM profile, model-specific launch setting, model-native GGUF context metadata. There is no universal numeric fallback. Missing metadata or inability to determine a safe automatic size produces an actionable error directing the user to supply an explicit context size; an explicit value still goes through applicable memory checks. Do not silently substitute a model-family window in local request budgeting.

The runtime resolves the planned value for a particular model and configuration, uses that same value for the memory projection and launch arguments, and confirms the actual per-request capacity before treating it as authoritative for inference. Distinguish total context allocation from per-slot/request capacity. Context flags must not silently override the resolved launch setting through duplicate or conflicting extra arguments.

Runtime-owned state records the model/instance association, resolved size and source, and whether the capacity is planned or confirmed. Budgeting must use confirmed capacity for the serving instance. A config edit cannot change the advertised capacity of an already-running instance. Stop, restart, replacement, failed startup, and adopted instances must not reuse stale confirmations. Missing confirmation must remain visibly unknown and cannot authorize a guessed request budget. Any necessary runtime/client/protocol changes must preserve this distinction.

Runtime-owned context is ephemeral process state, not a new persisted override. Setup may describe automatic resolution and its source without writing the resolved value as user configuration. The implementation must audit the existing launch-memory checks for the automatic metadata path so missing sizing evidence cannot silently become proof of safety.

## Decisions

### Preserve automatic policy rather than save a RAM-derived override

Approved by the user. Keep the existing model/RAM policy as the automatic source instead of freezing a global size during setup.

| Axis | Save a RAM-derived size | Preserve automatic selection — chosen |
|---|---|---|
| Model changes | One override can outlive the selected model | Resolve policy for the selected model |
| Catalog updates | Existing override masks updates | Automatic defaults can evolve |
| Implementation | Setup duplicates or freezes runtime policy | Setup preserves absence and uses runtime resolution |

### Optional integer configuration

Approved by the user. Use ContextSize *int, with nil meaning automatic, rather than retain the integer/presence-flag pair.

| Axis | Integer plus presence flag | Optional integer — chosen |
|---|---|---|
| Representation | Two fields can disagree | Absence is represented directly |
| Serialization | Requires special omission handling | Absent override omitted naturally |
| Cost and risk | Fewer caller changes but retains invariant | Audit callers, validation, and snapshot copying |
| Dependencies | No new external dependency | No new external dependency |

### Runtime-owned context

Approved by the user. Runtime resolution and confirmation own context state; budgeting consumes that state instead of independently reconstructing the serving window.

| Axis | Independently call a shared resolver | Runtime-owned context — chosen |
|---|---|---|
| Consistency | Inputs and config snapshots can differ | Capacity tied to the serving instance |
| Cost | Smaller interface change | Update runtime state and its budgeting interfaces |
| Risk | Can report a window the process does not serve | Must invalidate stale state and handle unknown capacity |
| Performance | Potential repeated metadata reads | Resolve and confirm during preparation/startup |
| Dependencies | No new external dependency expected | No new external dependency expected |

## Acceptance criteria

Regression tests first reproduce the absent-key save/reload defect, then demonstrate omission across repeated saves and setup persistence. Explicit 8192 and 65536 round-trip unchanged; explicit nonpositive values are rejected.

Resolver tests establish precedence, automatic GGUF fallback, actionable failure when unresolved, and preservation of deliberate per-model 8K settings. Memory tests prove the chosen size is the size checked and that unsafe automatic allocation fails before launch. No benchmark or parameter sweep is needed.

Runtime integration tests cover planned versus confirmed values, per-request capacity, startup failure, restart and invalidation, conflicting launch flags, and stale config versus running process. Tests must exercise the actual provider properties response shape; the installed llama-server version's native defaults are not assumed.

Budgeting and meter integration tests demonstrate that the serving instance's confirmed capacity is used, unknown local capacity is not promoted to a family maximum, and unrelated runtimes retain their behavior. Include the setup/config-to-launch path in integration coverage.

## Evidence and remaining verification

Inspected config loading/saving, the llama-server memory projection and GGUF parsing seam, modelwindow's independent resolver, and localruntime ModelRecord and InstanceRecord. InstanceRecord currently has no context-capacity field. Existing GGUF metadata extraction exposes ContextLength.

During implementation, verify startup readiness/properties access, adopted-instance identification, worker and dispatch propagation, launch-flag handling, and config pointer-copy behavior before editing their interfaces. A discovery that contradicts the approved runtime-owned design or safe pre-launch metadata premise requires a decision checkpoint, not an undocumented fallback.
