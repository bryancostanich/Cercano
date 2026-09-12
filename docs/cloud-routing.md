# Cloud profiles, destinations, and model quality

## Two separate choices

A **destination** chooses where a task runs. A **quality** chooses a model within that destination. Neither is a tool permission level.

| Task | Default destination | Default quality |
| --- | --- | --- |
| Chat | Primary | Premium |
| Default dispatch (class omitted) | Secondary | Premium |
| Reconnaissance | Local | Light |
| Mechanical development | Local | Standard |
| Investigation | Secondary | Premium |
| Implementation | Secondary | Premium |
| Review | Secondary | Premium |
| Research | Secondary | Premium |
| Git land | Local | Premium |
| Watchdog | Local | Standard |

Primary and Secondary each have their own preferred profile and optional backup. Secondary is not Primary's backup. Local uses its independently managed runtime and models. Light is the display label for the existing `economy` cost tier.

Secondary can redirect to Primary or Local; Local can redirect to Primary or Secondary. Chains are followed to their final destination; self-loops, cycles and unknown destinations are rejected atomically. **Own configuration** clears a redirect. Redirects change effective placement without overwriting saved profile bindings or task destination/quality. They are not failover: only the final destination's profile, model, credentials and backup chain are used.

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

## Editing settings

In **Routing** settings:

1. Choose Primary, Primary backup, Secondary and Secondary backup independently; **none** clears a binding.
2. Choose Secondary/Local redirects or **own configuration**. The effective route is displayed separately from the saved assignment.
3. Set each task's destination and quality, or leave either inherited. **Reset task** removes both overrides and restores that class's defaults.
4. **Save routing** applies the whole draft atomically. **Discard routing** restores saved assignments. Failed saves and disconnected-agent errors preserve edits.

In **Cloud**, edit profile credentials, quality and image choices using the separate profile save/discard actions. Routing controls no longer live on Cloud. Runtime/model management stays in **Runtime / Local Models**; Local does not have a cloud-profile binding.

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

To add backups, define their profiles and set `backup_cloud_profile` and/or `secondary_backup_cloud_profile` to their names. Missing references and preferred-equals-backup loops are rejected by the settings API.

## Setup wizard and verification limits

Wizard recommendations shown automatically remain inherited. Only explicit picker edits become profile overrides, and those edit markers survive wizard resume. Rollback snapshots preserve quality/image choices and provider/AWS metadata without capturing credentials. Older snapshots do not clear metadata they never recorded or restore retired profile-wide pins.

The implementation is verified with synthetic profiles, loopback HTTP/gRPC fixtures, real worker-process tests, and automated CLI form/transcript checks. Live paid inference and manual terminal visual inspection are separate deployment checks; no reliability or pricing claim follows from the fixtures. Restart the agent and its workers together after rebuilding the changed interfaces.
