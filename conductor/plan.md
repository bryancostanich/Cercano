# Master Project Plan

This plan tracks all major jobs for the project. Each job has its own detailed plan in its respective folder.

## [x] Track: Build the MVP of the local-first AI assistant, including the Go-based smart router with a gRPC interface for communication, and an initial IDE integration focused on a VS Code-compatible abstraction layer for Antigravity. [checkpoint: d4a2a76]

---

- [x] **Track: Improve the experience of cercano in the IDE with a more full-featured integration** [checkpoint: 88131af]
*Link: [./tracks/ide_enhancements_20260203/](./tracks/ide_enhancements_20260203/)*

---

- [~] **Track: Implement model-agnostic cloud integration for the Go backend using langchaingo**
*Link: [./tracks/cloud_integration_20260203/](./tracks/cloud_integration_20260203/)*

---

- [x] **Track: Fix broken VS Code code review and apply workflow** [checkpoint: 97e7b55]
*Link: [./tracks/ide_fixes_20260219/](./tracks/ide_fixes_20260219/)*

---

- [ ] **Track: Replace GenerationCoordinator with Google ADK LoopAgent**
*Link: [./tracks/adk_integration_20260219/](./tracks/adk_integration_20260219/)*

---

- [ ] **Track: SmartRouter classification improvements**
*Notes: "Tell me about this class" misclassified as Coding intent (similarity 0.5592). Explanation/analysis requests need better prototype coverage or a confidence threshold fallback.*
