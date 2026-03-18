# Master Project Plan

This plan tracks all major jobs for the project. Each job has its own detailed plan in its respective folder.

## [x] Track: Build the MVP of the local-first AI assistant, including the Go-based smart router with a gRPC interface for communication, and an initial IDE integration focused on a VS Code-compatible abstraction layer for Antigravity. [checkpoint: d4a2a76]

---

- [x] **Track: Improve the experience of cercano in the IDE with a more full-featured integration** [checkpoint: 88131af]
*Link: [./tracks/ide_enhancements_20260203/](./tracks/ide_enhancements_20260203/)*

---

- [x] **Track: Implement model-agnostic cloud integration for the Go backend using langchaingo** [checkpoint: a504f22]
*Link: [./tracks/cloud_integration_20260203/](./tracks/cloud_integration_20260203/)*

---

- [x] **Track: Fix broken VS Code code review and apply workflow** [checkpoint: 97e7b55]
*Link: [./tracks/ide_fixes_20260219/](./tracks/ide_fixes_20260219/)*

---

- [x] **Track: Replace GenerationCoordinator with Google ADK LoopAgent** [checkpoint: 58969fc]
*Link: [./tracks/adk_integration_20260219/](./tracks/adk_integration_20260219/)*

---

- [x] **Track: SmartRouter classification improvements** [checkpoint: 2365d75]
*Notes: Fixed by replacing single nearest-neighbor with top-K (K=3) average scoring per category, and stripping VS Code file context before embedding to prevent source code from skewing classification.*

---

- [x] **Track: Automatic Server Launch**
*Link: [./tracks/auto_server_launch_20260223/](./tracks/auto_server_launch_20260223/)*

---

- [x] **Track: Configurable Local Model**
*Link: [./tracks/configurable_local_model_20260223/](./tracks/configurable_local_model_20260223/)*

---

- [x] **Track: Token-Level LLM Streaming**
*Link: [./archive/token_streaming_20260223/](./archive/token_streaming_20260223/)*

---

- [ ] **Track: AI Engine Agnosticism — Abstract local inference layer to support pluggable backends**
*Link: [./tracks/engine_agnosticism_20260317/](./tracks/engine_agnosticism_20260317/)*

---

- [x] **Track: Cercano as MCP Server — Expose local inference as tools for cloud agents**
*Link: [./archive/mcp_server_20260317/](./archive/mcp_server_20260317/)*

---

- [x] **Track: Remote Inference — Runtime-configurable remote Ollama with model discovery and fallback**
*Link: [./archive/remote_inference_20260317/](./archive/remote_inference_20260317/)*

---

- [~] **Track: Local Co-Processor Tools — Specialized MCP tools for local offload (summarize, extract, classify, explain)**
*Link: [./tracks/local_coprocessor_tools_20260318/](./tracks/local_coprocessor_tools_20260318/)*

---

- [ ] **Track: Semantic Codebase Search — Embedding-based code search by intent**
*Link: [./tracks/semantic_search_20260318/](./tracks/semantic_search_20260318/)*

---

- [ ] **Track: Competitive Audit — Agent features landscape across open-source and commercial agents**
*Link: [./tracks/competitive_audit_20260318/](./tracks/competitive_audit_20260318/)*

---

- [ ] **Track: Agent Skills Integration — SKILL.md provider and consumer support**
*Link: [./tracks/agent_skills_20260318/](./tracks/agent_skills_20260318/)*

---

- [ ] **Track: AI Engine Agnosticism — Abstract local inference layer to support pluggable backends**
*Link: [./tracks/engine_agnosticism_20260317/](./tracks/engine_agnosticism_20260317/)*

---

- [ ] **Track: User-Friendly Distribution — Setup scripts, Docker packaging, and CI/CD releases**
*Link: [./tracks/distribution_20260317/](./tracks/distribution_20260317/)*
