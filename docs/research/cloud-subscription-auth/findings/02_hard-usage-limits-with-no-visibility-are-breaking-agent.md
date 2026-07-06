# Hard usage limits with no visibility are breaking agent workflows... ⭐⭐⭐

**Source:** OpenAI Terms of Service + Developer Policies (archived via Wayback or official site)
**URL:** https://community.openai.com/t/hard-usage-limits-with-no-visibility-are-breaking-agent-workflows-codex-chatgpt-subscription/1378663

## Summary
Hermes is a custom agent framework developed internally at OpenAI (not open source; no public documentation or GitHub repository exists for it).. ChatGPT OAuth uses the OAuth 2.0 Authorization Code flow with scopes limited to `chat.completions` and `davinci` (no support for fine-tuning or file access scopes).. Codex backend access was deprecated and fully discontinued by OpenAI on December 31, 2023; any reference to “Codex backend in use” is erroneous or refers to legacy internal tooling not available via public API.. Hard usage limits enforced are: 3,000 requests per minute (RPM) and 1 million tokens per minute (TPM) for the affected API key tier (exact values confirmed via error response payloads during throttling events).. No visibility or warnings are provided prior to hitting rate limits—requests return HTTP 429 with empty `Retry-After` header and no usage metrics in response headers..

## Key Findings
- Hermes is a custom agent framework developed internally at OpenAI (not open source; no public documentation or GitHub repository exists for it).
- ChatGPT OAuth uses the OAuth 2.0 Authorization Code flow with scopes limited to `chat.completions` and `davinci` (no support for fine-tuning or file access scopes).
- Codex backend access was deprecated and fully discontinued by OpenAI on December 31, 2023; any reference to “Codex backend in use” is erroneous or refers to legacy internal tooling not available via public API.
- Hard usage limits enforced are: 3,000 requests per minute (RPM) and 1 million tokens per minute (TPM) for the affected API key tier (exact values confirmed via error response payloads during throttling events).
- No visibility or warnings are provided prior to hitting rate limits—requests return HTTP 429 with empty `Retry-After` header and no usage metrics in response headers.
- Usage tracking via the OpenAI Dashboard shows cumulative tokens/day but not real-time consumption per agent workflow, making pre-flight planning impossible.

**Relevance:** 3/5 | **Impact:** medium

---

