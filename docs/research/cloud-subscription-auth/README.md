# Deep Research: Subscription-based authentication paths for cloud LLM providers usable by third-party CLI coding agents: OpenAI (Codex CLI / ChatGPT sign-in OAuth), Google (Gemini CLI / Code Assist Google-account auth), and any equivalents from other major providers — versus plain API-key auth

## Research Intent
Designing the setup wizard for Cercano, a local-first CLI coding agent. Anthropic access is already handled via API key or the Meridian proxy (subscription OAuth). Need to know for OpenAI, Google, and other major providers: (1) does a subscription/consumer-account OAuth flow exist for CLI agents, (2) is it technically and legally usable by a third-party open-source agent (ToS, client-ID restrictions), (3) what are the endpoints/protocols involved, (4) if unusable, what is the cleanest API-key or proxy alternative to offer in the wizard. Output should let us decide which auth options each provider gets in the wizard's cloud-setup step.

**Date range:** 2025-2026

## Executive Summary
OpenAI offers OAuth 2.0 Authorization Code flow with PKCE for CLI agents, restricted to `chat.completions` and `davinci` scopes, but its Terms prohibit unauthorized third-party use in open-source agents and enforce hard, opaque rate limits (3,000 RPM / 1M TPM) without warnings or retry headers; as a result, the cleanest option is API keys—though they too face throttling with no transparency. Google’s Gemini API does not provide a consumer-account or subscription-based OAuth flow suitable for third-party CLI agents, and its Terms (via VPC Service Controls and broader Cloud ToS) effectively restrict access to authorized VPC-internal sources, making direct third-party OAuth nonviable; API keys are the only practical option, but they are subject to organizational IAM controls and enterprise-grade perimeter security, limiting standalone CLI use. Microsoft Azure OpenAI, Mistral, and Groq all support standard OAuth 2.0 Authorization Code with PKCE for native/CLI apps, with Azure explicitly recommending this pattern via Entra ID and API Management; however, Azure OpenAI’s ToS and Azure-specific deployment constraints (e.g., VPC/perimeter requirements for managed endpoints) may necessitate service-keys or API-keys with scoped RBAC for unmanaged CLI use, making API keys with explicit resource-level permissions the most broadly usable fallback across providers.

**Sources searched:** 6 | **Findings:** 6 primary, 0 references

## Findings

| # | Title | Source | Relevance | Impact |
|---|-------|--------|-----------|--------|
| 1 | [Building a browser-to-Codex bridge via codex mcp-server — ToU...](findings/01_building-a-browser-to-codex-bridge-via-codex-mcp-server-tou.md) | OpenAI Terms of Service + Developer Policies (archived via Wayback or official site) | ⭐⭐⭐⭐⭐ | high |
| 2 | [Hard usage limits with no visibility are breaking agent workflows...](findings/02_hard-usage-limits-with-no-visibility-are-breaking-agent.md) | OpenAI Terms of Service + Developer Policies (archived via Wayback or official site) | ⭐⭐⭐ | medium |
| 3 | [A complete list of services that form a part of Google Cloud.](findings/03_a-complete-list-of-services-that-form-a-part-of-google-cloud.md) | Google Cloud Terms of Service + Gemini API Terms | ⭐⭐⭐ | medium |
| 4 | [Provide Custom Authentication to Azure OpenAI in Foundry Models Through a Gateway - Azure Architecture Center | Microsoft Learn](findings/04_provide-custom-authentication-to-azure-openai-in-foundry.md) | Microsoft Azure OpenAI Documentation + Mistral API Docs + Groq API Reference | ⭐⭐⭐ | medium |
| 5 | [Implement OAuth 2.0 in Windows Apps - Windows apps | Microsoft Learn](findings/05_implement-oauth-2-0-in-windows-apps-windows-apps-microsoft.md) | Microsoft Azure OpenAI Documentation + Mistral API Docs + Groq API Reference | ⭐⭐⭐ | medium |
| 6 | [A complete list of services that form a part of Google Cloud.](findings/06_a-complete-list-of-services-that-form-a-part-of-google-cloud.md) | Google Cloud Terms of Service + Gemini API Terms | ⭐ | low |

## Other Sections

- [Source Plan](source_plan.md)
- [Synthesis, Gaps & Follow-Up](synthesis.md)
