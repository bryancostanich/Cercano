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
