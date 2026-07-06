# Source Plan: Subscription-based authentication paths for cloud LLM providers usable by third-party CLI coding agents: OpenAI (Codex CLI / ChatGPT sign-in OAuth), Google (Gemini CLI / Code Assist Google-account auth), and any equivalents from other major providers — versus plain API-key auth

## 1. OpenAI Developer Documentation
**Why:** The official API docs are the only authoritative source for OAuth flows, client registration, token scopes, and ToS-compliant usage patterns for CLI integrations.

**Queries:**
- "OAuth 2.0" "CLI tool" "Codex CLI" site:platform.openai.com
- "PKCE" "device authorization grant" "third-party" "CLI" 2025..2026 site:platform.openai.com

## 2. OpenAI Terms of Service + Developer Policies (archived via Wayback or official site)
**Why:** Legal compliance hinges on direct reading of current ToS, especially clauses on third-party integrations, OAuth usage, and prohibited use (e.g., “automated systems” restrictions, prohibited redistribution of credentials).

**Queries:**
- "third-party" "CLI" "OAuth" " Terms of Service" "open source" 2025..2026 site:openai.com
- "prohibited use" "agent" "CLI tool" "OAuth" site:openai.com

## 3. Google Cloud Identity Platform Documentation + Google Developers Site
**Why:** Google’s OAuth setup for desktop/CLI apps (using the “Installed App” client type and localhost redirect) is documented here; Code Assist CLI access relies on these same OAuth flows as of 2025.

**Queries:**
- "Gemini CLI" "OAuth" "installed application" "localhost redirect" 2025..2026 site:cloud.google.com
- "code assist" "CLI" "third-party" "OAuth 2.0" "google-auth-library" site:google.com

## 4. Google Cloud Terms of Service + Gemini API Terms
**Why:** ToS compliance is required before recommending OAuth in a public CLI tool; exceptions for open-source or non-Google tools must be explicitly stated or implied.

**Queries:**
- "Gemini API" "Terms of Service" "third-party" "open source" "CLI" 2025..2026 site:cloud.google.com
- "Code Assist" "ToS" "CLI agent" "OAuth" "redistribution" 2025 site:developers.google.com

## 5. Microsoft Azure OpenAI Documentation + Mistral API Docs + Groq API Reference
**Why:** These providers publish their auth flows, CLI support, and ToS compliance notes — and all are active in 2025–2026 as per recent industry adoption.

**Queries:**
- "Azure OpenAI" "CLI" "OAuth" "desktop app" "PKCE" 2025..2026 site:learn.microsoft.com
- "Mistral AI" "CLI authentication" "OAuth" "third-party" "ToS" 2025 site:mistral.ai
- "Groq API" "CLI" "user authentication" "OAuth" 2025..2026 site:console.groq.com

## 6. GitHub Discussions / Stack Overflow (for community-reported workarounds) + Provider-specific changelogs
**Why:** Provider docs may not explicitly call out workarounds, but GitHub issues and SE discussions often surface *de facto* patterns (e.g., how open-source tools handle CLI auth where OAuth is blocked).

**Queries:**
- "CLI agent" "API key vs OAuth" "third-party" "blocked" 2025..2026 site:github.com
- "Code assist CLI" "not working with OAuth" "API key fallback" 2025..2026 site:stackoverflow.com

---

**Total:** 6 primary findings, 0 discovered references
