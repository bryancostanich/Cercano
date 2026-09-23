# Model tiers, cloud profiles, and model quality

## Two separate choices

A **model tier** (called a `destination` in configuration and APIs) chooses where a task runs. A **quality** chooses a model within that destination. Neither is a tool permission level.

| Task | Default model tier | Default quality |
| --- | --- | --- |
| Chat | Primary | Standard |
| Default dispatch (class omitted) | Secondary | Premium |
| Reconnaissance | Local | Light |
| Mechanical development | Local | Standard |
| Investigation | Secondary | Premium |
| Implementation | Secondary | Premium |
| Review | Secondary | Premium |
| Research | Secondary | Premium |
| Git land | Local | Premium |
| Watchdog | Local | Standard |

Primary has a preferred account and an ordered list of backup accounts; Secondary keeps its own preferred profile and single optional backup. Secondary is not Primary's backup. Local uses its independently managed runtime and models. Light is the display label for the existing `economy` cost tier.

Secondary can redirect to Primary or Local; Local can redirect to Primary or Secondary. Chains are followed to their final destination; self-loops, cycles and unknown destinations are rejected atomically. **No redirect** clears a redirect. Redirects change effective placement without overwriting saved profile bindings or task destination/quality. They are not failover: only the final destination's profile, model, credentials and backup chain are used.

Final Primary placement follows locus policy. `open_only` prohibits cloud inference and `cloud_only` prohibits Local inference. Final Secondary never automatically falls through to Primary or Local; final Local has no implicit cloud fallback. Final Primary retains its existing permitted fallback, including redirected calls. Visible output and completed tool work must not be replayed across destinations.

Omitting a task class uses Default dispatch, regardless of role, source label or prompt wording. Explicit dispatch difficulty changes quality only: light→Light, standard→Standard, deep→Premium. Standalone Review, Research and Git land carry their own class; watchdog is a normal configurable task, not an exemption. Text-analysis helpers use Reconnaissance; the deprecated co-processor wire flag is a compatibility alias for ordinary Default dispatch. The explicitly local `local` tool retains its prior policy rather than inheriting Default dispatch.

Profile names identify configuration and credentials, not hardware. Selecting a destination does not imply GPU eviction or reconfigure a local runtime.

## Quality and image choices belong to profiles

Each cloud profile has sparse `economy`, `standard`, and `premium` overrides. An absent override follows the shipped recommendation for that provider. Reset removes the override; it does not copy today's recommendation into your configuration. Two profiles from the same provider can therefore have different choices.

DeepInfra recommendations are:

- Economy: `openai/gpt-oss-120b`
- Standard: `zai-org/GLM-5.3-Flash`
- Premium: `zai-org/GLM-5.3`

These are product defaults, not measured reliability or price claims. Discovery uses the registered DeepInfra catalog and its cache. A selected/custom ID remains editable if discovery fails or omits it.

Image inspection uses the profile's separate `image_model`, never its text quality selection. No image model is invented when the selection is empty. Image bytes are sent only when the selected model has confirmed image-input support for that profile/endpoint. A backup must have its own image choice and confirmation. A confirmed configured backup is considered before Local when the preferred image model is missing or unconfirmed. Inspection remains a separate, tool-free request over the existing conversation-scoped attachment store. Successful-answer cache keys are unchanged; an identical question can reuse a previous answer after a model selection changes.

Legacy profile-wide `model`/`model_pinned` choices no longer override quality selection and are not migrated into every quality slot. The legacy `UpdateConfig.cloud_model` mutation is rejected. Re-select intentional choices in the Cloud profile editor.

## Multiple accounts on one provider

A provider can hold several independently authenticated accounts. In **Cloud**, a configured provider row offers **Add another account**, which starts a distinct named account draft that copies only connection structure — never credentials, keys or quality choices. Each subscription account keeps its own sign-in action, so any account can be refreshed without touching another. Adding an account never replaces an existing account with the same name and never changes the current Primary selection; reauthenticating an existing account preserves its saved model and image choices.

When the account serving Primary exhausts its quota, Primary advances to the next configured backup account and stays there for later requests rather than returning on a timer. Traversal visits each configured account at most once per request, wraps around to earlier accounts after the list end, and stops with `all configured cloud accounts exhausted` when every account reports quota exhaustion. Other failure classes keep their existing behavior: transient errors still get their bounded retry, and authentication problems still surface their normal recovery prompt. A quota failure after output has already been streamed does not replay that response; it only affects which account serves the next request. The active account survives unrelated provider reconfiguration and resets to the preferred account when the configured list no longer contains it.

## Editing settings

The **Routing** tab has two sections:

### Model tiers

Primary groups its **Account** selection with an ordered backup list, each entry offering **Move up**, **Move down**, and **Remove backup**, plus a trailing **Add backup** selector. Secondary groups its profile and single backup control. Selections show provider and account name, so several accounts from one provider stay distinguishable, and an account already used in that tier is not offered again. An empty Primary account or Secondary **No backup** is an absent binding, not a hidden default selection.

Secondary and Local each have **Redirect all work to**, with **No redirect** as the normal selection. A redirect always changes where work runs; a backup is tried after a failure. Local's runtime/model setup remains in **Runtime / Local Models**, rather than becoming a cloud-profile binding.

### Task routing

Each task has one compact row with independently editable **Model tier** and **Quality** columns. On a focused row, use left/right to select a column, then Enter to choose a value. Narrow terminals stack the labeled columns instead of clipping them.

The row shows actual values, including defaults—not “inherit” or “unset.” A value that differs from that task's built-in default is marked **(overridden)**. Destination and quality can be overridden independently. Choosing the default removes that component's override so it follows future defaults. On a modified task, **r Restore defaults** removes both overrides; the action and default-value help are shown only for the focused task.

There is no permanent “effective” row. A note such as **Redirected to Primary** appears only when a redirect changes the task's destination. This note is not a report of runtime failover or which provider served a request.

**Save routing** applies the whole draft atomically; **Discard routing** restores saved assignments. The page distinguishes **Unsaved changes** from saved overrides. Selecting an unchanged value or undoing edits back to the saved state does not mark the page unsaved. Save/Discard are disabled when there are no pending changes. Failed saves and disconnected-agent errors preserve edits.

In **Cloud**, edit profile credentials, quality and image choices using the separate profile Save/Discard actions. Cloud no longer displays Primary/backup routing badges or supports the obsolete activation/backup actions; authentication and account identity annotations remain. Routing is the sole owner of tier bindings and backups.

All choice edits are drafts, including existing profiles. Cancelling a picker keeps the original value. Unsaved edits prompt before leaving their page or changing profiles; cancelling navigation preserves the draft. Cloud and Routing save/discard operations do not apply or clear each other's draft. Authentication and API-key operations remain separate actions.

Activating a profile does not silently make the previous Primary its backup. If the requested Primary is already the Primary backup, use the routing draft to change both bindings atomically instead of creating a self-loop.

Validation failures retain the draft. A valid routing or profile save can succeed while a selected provider is unavailable; that condition is reported as an availability warning, not as an unsaved draft.

**Local Models** contains downloadable/local-runtime models only. Hosted catalog entries do not enter Local download or RAM-estimate paths. Existing source-qualified model references, embedding choices, and local runtime overrides remain in use.

## Explicit dispatch overrides

`light`, `standard`, and `deep` select Economy, Standard, and Premium for that invocation. Omitted difficulty uses the saved dispatch quality; unknown difficulty keeps the prior Economy behavior. Existing invocation-specific model overrides remain supported, but a backup re-resolves its own model at the requested quality instead of receiving the originating profile's model ID.

## Failure, refresh, and attribution

Each destination fails over only within its own configured chain. Credentials are fetched by actual profile identity. Editing or removing any referenced profile, rotating its key, or changing assignments refreshes future provider chains. Workers receive a fresh configuration snapshot per turn, including all referenced profiles, saved tasks, effective Local vision, and scoped model evidence; existing turns are not silently rebound.

Per-attempt metadata records the actual profile/model and its context evidence, including backup requests. Identical model IDs on different endpoints do not make their capacity or image evidence interchangeable. Unknown context retains the conventional unverified fallback rather than borrowing another endpoint's evidence.

Automatic whole-turn retries and cross-destination fallback stop once visible text or tool execution begins. An interrupted response is reported rather than replaying output or tool side effects. Eligible failures before that boundary retain existing retry behavior.

## YAML example

This example configures two preferred profiles and deliberately leaves both backups unset. API keys stay in the existing credential store, not in this YAML.

```yaml
active_cloud_profile: main-deepinfra
secondary_cloud_profile: dispatch-deepinfra
task_assignments:
  chat:
    destination: primary
    quality: premium
  dispatch:
    destination: secondary
    quality: premium
cloud_profiles:
  - name: main-deepinfra
    flavor: chat_completions
    backend: openai
    provider: deepinfra
    base_url: https://api.deepinfra.com/v1/openai
  - name: dispatch-deepinfra
    flavor: chat_completions
    backend: openai
    provider: deepinfra
    base_url: https://api.deepinfra.com/v1/openai
    tier_overrides:
      economy: openai/gpt-oss-120b
```

To add backups, define their profiles and set `backup_cloud_profiles` (ordered list, Primary), `backup_cloud_profile` (single legacy Primary spelling, kept in step with the list's first entry) and/or `secondary_backup_cloud_profile` to their names. Missing references, empty backup entries, and any duplicate account within one tier are rejected by the settings API, so an account never fails over to itself. An older single-backup configuration keeps working: the legacy field loads as the first backup, and clearing the list clears both spellings.

## Setup wizard and verification limits

Wizard recommendations shown automatically remain inherited. Only explicit picker edits become profile overrides, and those edit markers survive wizard resume. Rollback snapshots preserve quality/image choices and provider/AWS metadata without capturing credentials. Older snapshots do not clear metadata they never recorded or restore retired profile-wide pins.

The implementation is verified with synthetic profiles, loopback HTTP/gRPC fixtures, real worker-process tests, and automated CLI form/transcript checks. Live paid inference and manual terminal visual inspection are separate deployment checks; no reliability or pricing claim follows from the fixtures. Restart the agent and its workers together after rebuilding the changed interfaces.
