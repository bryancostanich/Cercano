# Master Project Plan

This plan tracks all major jobs for the project. Each job links to its detailed
spec or plan under `features/`, `plans/`, `research/`, or `internal/`.

## [x] Track: Build the MVP of the local-first AI assistant, including the Go-based smart router with a gRPC interface for communication, and an initial IDE integration focused on a VS Code-compatible abstraction layer for Antigravity. [checkpoint: d4a2a76]

*Link: [./features/local_ai_mvp_spec.md](./features/local_ai_mvp_spec.md)*

---

- [x] **Track: Improve the experience of cercano in the IDE with a more full-featured integration** [checkpoint: 88131af]
*Link: [./features/ide_enhancements_spec.md](./features/ide_enhancements_spec.md)*

---

- [x] **Track: Implement model-agnostic cloud integration for the Go backend using langchaingo** [checkpoint: a504f22]
*Link: [./features/cloud_integration_spec.md](./features/cloud_integration_spec.md)*

---

- [x] **Track: Fix broken VS Code code review and apply workflow** [checkpoint: 97e7b55]
*Link: [./internal/ide_fixes.md](./internal/ide_fixes.md)*

---

- [x] **Track: Replace GenerationCoordinator with Google ADK LoopAgent** [checkpoint: 58969fc]
*Link: [./features/adk_integration_spec.md](./features/adk_integration_spec.md)*

---

- [x] **Track: SmartRouter classification improvements** [checkpoint: 2365d75]
*Notes: Fixed by replacing single nearest-neighbor with top-K (K=3) average scoring per category, and stripping VS Code file context before embedding to prevent source code from skewing classification.*

---

- [x] **Track: Automatic Server Launch**
*Link: [./features/auto_server_launch_spec.md](./features/auto_server_launch_spec.md)*

---

- [x] **Track: Configurable Local Model**
*Link: [./features/configurable_local_model_spec.md](./features/configurable_local_model_spec.md)*

---

- [x] **Track: Token-Level LLM Streaming**
*Link: [./features/token_streaming_spec.md](./features/token_streaming_spec.md)*

---

- [x] **Track: Cercano as MCP Server — Expose local inference as tools for cloud agents**
*Link: [./features/mcp_server_spec.md](./features/mcp_server_spec.md)*

---

- [x] **Track: Remote Inference — Runtime-configurable remote Ollama with model discovery and fallback**
*Link: [./features/remote_inference_spec.md](./features/remote_inference_spec.md)*

---

- [x] **Track: Local Co-Processor Tools — Specialized MCP tools for local offload (summarize, extract, classify, explain)**
*Link: [./features/local_coprocessor_tools_spec.md](./features/local_coprocessor_tools_spec.md)*

---

- [ ] **Track: Semantic Codebase Search — Embedding-based code search by intent**
*Link: [./plans/semantic_search.md](./plans/semantic_search.md)*

---

- [ ] **Track: Competitive Audit — Agent features landscape across open-source and commercial agents**
*Link: [./research/competitive_audit.md](./research/competitive_audit.md)*

---

- [x] **Track: Agent Skills Integration — SKILL.md provider support** [checkpoint: 90c74d1]
*Link: [./features/agent_skills_spec.md](./features/agent_skills_spec.md)*
*Notes: Provider-side complete — 7 skills published, cercano_skills MCP tool, docs. Consumer-side skill discovery deferred to a future track pending real-world testing with third-party skills.*

---

- [x] **Track: AI Engine Agnosticism — Abstract local inference layer to support pluggable backends**
*Link: [./features/engine_agnosticism_spec.md](./features/engine_agnosticism_spec.md)*
*Notes: Shipped via PR #1 (merged 2026-03-25).*

---

- [x] **Track: User-Friendly Distribution — Setup scripts, Docker packaging, and CI/CD releases**
*Link: [./features/distribution_spec.md](./features/distribution_spec.md)*
*Notes: Largely shipped; a few sub-items remain (see "Remaining" in the spec).*

---

- [ ] **Track: Docker Deployment — Containerized Cercano with Docker and docker-compose**
*Link: [./plans/docker.md](./plans/docker.md)*

---

- [x] **Track: Usage Telemetry & Token Savings Metrics**
*Link: [./features/usage_telemetry_spec.md](./features/usage_telemetry_spec.md)*

---

- [x] **Track: Project Context Initialization** [checkpoint: 866f53d]
*Link: [./features/project_context_spec.md](./features/project_context_spec.md)*

---

- [x] **Track: cercano_document — Local Code Documentation Tool**
*Link: [./features/document_tool_spec.md](./features/document_tool_spec.md)*

---

- [x] **Track: Update Check & Upgrade Prompt**
*Link: [./features/update_check_spec.md](./features/update_check_spec.md)*

---

- [ ] **Track: Cloud Token Savings Estimation — Measure actual tokens kept out of cloud context**
*Link: [./plans/savings_estimation.md](./plans/savings_estimation.md)*

---

- [x] **Track: Deep Research Skill — Multi-source academic research with ranked, annotated findings**
*Link: [./features/deep_research_spec.md](./features/deep_research_spec.md)*

---

- [ ] **Track: Deep Research Enhancement — Multi-pass analysis, cross-finding context, quality gating**
*Link: [./plans/deep_research_enhancement.md](./plans/deep_research_enhancement.md)*

---

- [ ] **Track: Plugin Packaging — Claude Code, Gemini CLI, Codex CLI plugin/extension packages**
*Link: [./plans/plugin_packaging.md](./plans/plugin_packaging.md)*
