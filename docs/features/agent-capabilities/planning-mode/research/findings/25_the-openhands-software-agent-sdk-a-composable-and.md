# The OpenHands Software Agent SDK: A Composable and Extensible Foundation for Production Agents ⭐⭐⭐⭐⭐

**Source:** arXiv (CS preprints) + GitHub (Open source implementations)
**URL:** https://arxiv.org/html/2511.03690v1

## Summary
November 5, 2025. Blocking parallel execution. Implemented as a standard tool in the openhands.tools package. Parent agent spawns and monitors sub-agents until all tasks complete. Complex coordination behaviors such as asynchronous delegation, dynamic scheduling, or fault-tolerant recovery can be implemented entirely as user-defined tools.

## Key Findings
- November 5, 2025
- Blocking parallel execution
- Implemented as a standard tool in the openhands.tools package
- Parent agent spawns and monitors sub-agents until all tasks complete
- Complex coordination behaviors such as asynchronous delegation, dynamic scheduling, or fault-tolerant recovery can be implemented entirely as user-defined tools
- SDK’s design principle: extensibility for advanced agent orchestration requires no modification to the core framework

## Why This Matters
The OpenHands SDK provides a robust, composable foundation for building production-grade agents with clear separation between planning and execution, which directly supports Cercano’s goal of designing a holistic planning mode and a distinct plan-execution skill. The fact that the SDK enables parent agents to spawn and monitor sub-agents until all tasks complete mirrors the desired workflow of a high-level planner delegating to an executor and receiving feedback. Additionally, the ability to implement complex coordination behaviors—such as asynchronous delegation, dynamic scheduling, and fault-tolerant recovery—entirely as user-defined tools means Cercano can prototype and refine its own gating logic, mid-run extension logic, and handoff protocols without touching the core framework. This aligns perfectly with Cercano’s intent to extract best practices in how agent systems manage the lifecycle of plans and execution. Crucially, the design principle that “extensibility for advanced agent orchestration requires no modification to the core framework” implies that Cercano can experiment with different planning-to-execution flows (e.g., interactive convergence + structured artifact capture + feedback loops) in a modular, reusable way—exactly what Cercano is trying to achieve.

**Connections to other findings:** This extends prior findings on *Claude Code’s Plan Mode* (e.g., points 2, 5, and 6), which is read-only and reaction-based, limiting its ability to support dynamic plan extension. OpenHands, by contrast, enables *control flow* and *feedback loops*, allowing the plan to evolve during execution—the very mechanism Cercano is seeking. It also connects with *Exec Plan - AI-First SSOT* (point 13) by showing that structured artifact management (e.g., task hierarchy, format) is feasible and maintainable if designed at the SDK level, not as a byproduct. Unlike *Aider’s Architect Mode* (points 7–13), which treats planning as a fixed mode, OpenHands treats the planning-execution interface as a *programmable interface*, allowing Cercano to evaluate and test multiple design flavors of the planning loop.

**Relevance:** 5/5 | **Impact:** high

---

