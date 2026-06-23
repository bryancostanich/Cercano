# Master Project Plan

This plan tracks all major jobs for the project. Each job links to its detailed
spec or plan under `features/`, `research/`, or `internal/`. Status lives in each
doc's header; folders no longer move when a feature ships.

## [x] Track: Build the MVP of the local-first AI assistant, including the Go-based smart router with a gRPC interface for communication, and an initial IDE integration focused on a VS Code-compatible abstraction layer for Antigravity. [checkpoint: d4a2a76]

*Link: [../features/local-ai-mvp/spec.md](../features/local-ai-mvp/spec.md)*

---

- [x] **Track: Improve the experience of cercano in the IDE with a more full-featured integration** [checkpoint: 88131af]
*Link: [../features/ide-enhancements/spec.md](../features/ide-enhancements/spec.md)*

---

- [x] **Track: Implement model-agnostic cloud integration for the Go backend using langchaingo** [checkpoint: a504f22]
*Link: [../features/cloud-integration/spec.md](../features/cloud-integration/spec.md)*

---

- [x] **Track: Fix broken VS Code code review and apply workflow** [checkpoint: 97e7b55]
*Link: [../internal/ide-fixes.md](../internal/ide-fixes.md)*

---

- [x] **Track: Replace GenerationCoordinator with Google ADK LoopAgent** [checkpoint: 58969fc]
*Link: [../features/adk-integration/spec.md](../features/adk-integration/spec.md)*

---

- [x] **Track: SmartRouter classification improvements** [checkpoint: 2365d75]
*Notes: Fixed by replacing single nearest-neighbor with top-K (K=3) average scoring per category, and stripping VS Code file context before embedding to prevent source code from skewing classification.*

---

- [x] **Track: Automatic Server Launch**
*Link: [../features/auto-server-launch/spec.md](../features/auto-server-launch/spec.md)*

---

- [x] **Track: Configurable Local Model**
*Link: [../features/configurable-local-model/spec.md](../features/configurable-local-model/spec.md)*

---

- [x] **Track: Token-Level LLM Streaming**
*Link: [../features/token-streaming/spec.md](../features/token-streaming/spec.md)*

---

- [x] **Track: Cercano as MCP Server — Expose local inference as tools for cloud agents**
*Link: [../features/mcp-server/spec.md](../features/mcp-server/spec.md)*

---

- [x] **Track: Remote Inference — Runtime-configurable remote Ollama with model discovery and fallback**
*Link: [../features/remote-inference/spec.md](../features/remote-inference/spec.md)*

---

- [x] **Track: Local Co-Processor Tools — Specialized MCP tools for local offload (summarize, extract, classify, explain)**
*Link: [../features/local-coprocessor-tools/spec.md](../features/local-coprocessor-tools/spec.md)*

---

- [ ] **Track: Semantic Codebase Search — Embedding-based code search by intent**
*Link: [../features/semantic-search/plan.md](../features/semantic-search/plan.md)*

---

- [ ] **Track: Competitive Audit — Agent features landscape across open-source and commercial agents**
*Link: [../research/competitive-audit.md](../research/competitive-audit.md)*

---

- [x] **Track: Agent Skills Integration — SKILL.md provider support** [checkpoint: 90c74d1]
*Link: [../features/agent-skills/spec.md](../features/agent-skills/spec.md)*
*Notes: Provider-side complete — 7 skills published, cercano_skills MCP tool, docs. Consumer-side skill discovery deferred to a future track pending real-world testing with third-party skills.*

---

- [x] **Track: AI Engine Agnosticism — Abstract local inference layer to support pluggable backends**
*Link: [../features/engine/agnosticism.md](../features/engine/agnosticism.md)*
*Notes: Shipped via PR #1 (merged 2026-03-25).*

---

- [x] **Track: User-Friendly Distribution — Setup scripts, Docker packaging, and CI/CD releases**
*Link: [../features/distribution/spec.md](../features/distribution/spec.md)*
*Notes: Largely shipped; a few sub-items remain (see "Remaining" in the spec).*

---

- [ ] **Track: Docker Deployment — Containerized Cercano with Docker and docker-compose**
*Link: [../features/docker/plan.md](../features/docker/plan.md)*

---

- [x] **Track: Usage Telemetry & Token Savings Metrics**
*Link: [../features/usage-telemetry/spec.md](../features/usage-telemetry/spec.md)*

---

- [x] **Track: Project Context Initialization** [checkpoint: 866f53d]
*Link: [../features/project-context/spec.md](../features/project-context/spec.md)*

---

- [x] **Track: cercano_document — Local Code Documentation Tool**
*Link: [../features/document-tool/spec.md](../features/document-tool/spec.md)*

---

- [x] **Track: Update Check & Upgrade Prompt**
*Link: [../features/update-check/spec.md](../features/update-check/spec.md)*

---

- [ ] **Track: Cloud Token Savings Estimation — Measure actual tokens kept out of cloud context**
*Link: [../features/savings-estimation/plan.md](../features/savings-estimation/plan.md)*

---

- [x] **Track: Deep Research Skill — Multi-source academic research with ranked, annotated findings**
*Link: [../features/deep-research/spec.md](../features/deep-research/spec.md)*

---

- [ ] **Track: Deep Research Enhancement — Multi-pass analysis, cross-finding context, quality gating**
*Link: [../features/deep-research/enhancement-plan.md](../features/deep-research/enhancement-plan.md)*

---

- [ ] **Track: Plugin Packaging — Claude Code, Gemini CLI, Codex CLI plugin/extension packages**
*Link: [../features/plugin-packaging/plan.md](../features/plugin-packaging/plan.md)*
