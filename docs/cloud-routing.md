# Cloud profiles, destinations, and model quality

## Two separate choices

A **destination** chooses where a task runs. A **quality** chooses a model within that destination. Neither is a tool permission level.

| Task | Default destination | Default quality |
| --- | --- | --- |
| Main chat | Primary | Premium |
| Explicit dispatch/sub-agent | Secondary | Premium |

Primary and Secondary each have their own preferred profile and optional backup. Secondary is not another name for Primary's backup. Local uses the independently managed local runtime and its existing model recommendations/overrides.

Primary placement continues to obey the existing locus policy. `open_only` prohibits cloud inference and `cloud_only` prohibits Local inference. Secondary does not automatically fall through to Primary or Local. If Secondary is unconfigured, prohibited by locality, or exhausted, explicit dispatch reports it unavailable. Other co-processor work retains its existing routing policy.

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

## Editing settings

In **Cloud** settings:

1. Select Primary, Primary backup, Secondary, and Secondary backup independently. Choose **none** to clear a binding.
2. Set chat and dispatch destinations/qualities, or leave their fields inherited.
3. Use **Save routing** to apply that complete assignment draft, or **Discard routing** to restore the saved state.
4. Select a profile to edit its quality and image choices. Use the separate profile **save** or **discard** action.

Choice edits are drafts, including edits to existing profiles. Cancelling a picker does not change a draft. Unsaved profile edits prompt before changing profiles; unsaved Cloud drafts prompt before leaving the tab or closing the surface. Authentication, API-key operations, and explicit profile activation remain separate actions.

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

To add backups, define their profiles and set `backup_cloud_profile` and/or `secondary_backup_cloud_profile` to their names. Missing references and preferred-equals-backup loops are rejected by the settings API.

## Setup wizard and verification limits

Wizard recommendations shown automatically remain inherited. Only explicit picker edits become profile overrides, and those edit markers survive wizard resume. Rollback snapshots preserve quality/image choices and provider/AWS metadata without capturing credentials. Older snapshots do not clear metadata they never recorded or restore retired profile-wide pins.

The implementation is verified with synthetic profiles, loopback HTTP/gRPC fixtures, real worker-process tests, and automated CLI form/transcript checks. Live paid inference and manual terminal visual inspection are separate deployment checks; no reliability or pricing claim follows from the fixtures. Restart the agent and its workers together after rebuilding the changed interfaces.
