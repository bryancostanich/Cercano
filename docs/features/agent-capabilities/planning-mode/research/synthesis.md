## Synthesis
Cercano’s design of a holistic planning mode—combining interactive brainstorming with structured plan capture and a separate execution layer—finds strong validation in the best-in-class practices observed across state-of-the-art AI coding tools like Claude Code and Aider. A central theme emerging from these findings is the powerful separation of *planning* and *execution*, a design principle that significantly enhances reliability, safety, and efficiency. In Claude Code’s Plan Mode, the system acts as a read-only researcher, using Claude 3 Opus to analyze codebases through a dedicated subagent that inspects files without making changes. This ensures safety, mitigates accidental mutations, and aligns with the core purpose of a planning phase: exploration and logic validation. Similarly, Aider’s architect mode adopts this two-stage workflow, where a high-level planning model proposes changes—such as modularization of a Rust codebase—before a separate, more precise editor model applies the actual code edits. This division reduces error propagation, improves plan quality, and enables earlier human review, as demonstrated by Aider’s ability to identify 14 functional groups in a complex file with 94.3% accuracy. The recurring insight is that high-level reasoning, creative structuring, and strategic thinking are best served by powerful, expensive models (like Opus or GPT-5), while the lower-level implementation is better handled by faster, specialized models—resulting in up to 57% less rework and 35% reduced debugging time.

The significance of these findings extends into how plans are articulated, stored, and handed off. Both Claude Code and Aider emphasize the creation of a *structured, human-readable, editable artifact*, often exposing the plan via a default text editor (VS Code, Vim, etc.), allowing users to inspect, adjust, and approve the plan before any changes are enacted. This mirrors the importance of a shared, versioned “Single Source of Truth” (SSOT) with clear semantics—such as RFC 3339 timestamps and explicit phase boundaries—ensuring traceability and accountability. Crucially, both systems embed *approval gates* at critical junctures: either before the planning process begins (as enforced by Claude’s read-only design) or during handoff to execution (as with Aider and CreateOS, which embed review checkpoints within the workflow). This reduces context switching and maintains runtime continuity, as the orchestrator retains control and feeds sub-agent outputs back through the system rather than directly to the human, preserving the integrity of the process. Ultimately, these tools show that true agility comes not from speed alone, but from a resilient cycle: human-in-the-loop supervision, structured plan capture, validated phase-by-phase progression, and seamless feedback loops that allow execution to report back and reopen the plan mid-run. For Cercano, this signals an optimal path: build a de novo system that treats planning as a *collaborative, artifact-driven, milestone-anchored process*, leveraging best practices in mode separation, modularity, human oversight, and feedback integration to create not just a smarter assistant, but a trustworthy co-architect.

## Contradictions & Open Debates
There are several contradictions and contested claims across the provided research findings. Below is a summary of the key conflicts:

1. **Mode Activation and User Interaction (Findings 4 vs. 5 vs. 6)**  
   - **Conflict**: Disagreement on how Plan Mode is activated and whether it requires user approval.  
   - **Detail**:  
     - Finding 4 states users press `Ctrl+G` to open the plan in a text editor (implying direct, manual editing within a supported editor).  
     - Finding 5 says Plan Mode is entered by pressing `Shift+Tab` twice or using a CLI command (`claude --permission-mode plan`), suggesting a direct system-level trigger.  
     - Finding 6 emphasizes that Plan Mode is "read-only by design" and that “Claude cannot edit, create, or execute anything,” but does not mention user-initiated editing in an external editor.  
     - The contradiction lies in conflicting methodological instructions (Ctrl+G vs. Shift+Tab vs. CLI) and the implication that users may edit the plan in an external editor (finding 4) versus the claim that Claude cannot perform any actions, raising ambiguity about whether user edits are permitted or mediated.

2. **Agent Capabilities and Permissions During Plan Mode (Findings 2 vs. 5 vs. 6)**  
   - **Conflict**: Disagreement on whether the subagent in Plan Mode can perform any actions or is truly restricted to read-only operations.  
   - **Detail**:  
     - Finding 2 states the “Explore subagent” reads files and is “restricted to read-only tools,” which aligns with safety.  
     - Finding 5 and 6 both assert Plan Mode is “read-only by design” and Claude “cannot edit, create, or execute anything,” reinforcing the safety premise.  
     - However, Finding 3 (which claims 92% task completion and 57% code rework reduction) implies that Plan Mode is not just viewing but also *verifying logic* and *anticipating execution*, possibly involving predictive or pre-execution analysis that might blur the line between “reading” and “acting.” This is not clearly contradicted, but the high-performing outcomes in finding 3 could suggest a more active role than "read-only," which contradicts the safety emphasis in 5 and 6 if the analysis includes modeling or simulation.

3. **Role of Human Approval and Coordination in the Workflow (Findings 13 vs. 14 vs. 15)**  
   - **Conflict**: Contradictory views on the necessity and structure of human approval in AI agent workflows.  
   - **Detail**:  
     - Finding 13 criticizes the plan for “assuming the agent can just run X” without stating approvals, saying that “tooling differs across runtimes,” implying lack of safeguards.  
     - Finding 14 argues for embedding approval gates directly into the execution layer to reduce context switching.  
     - Finding 15 advocates for “orchestrating agent surfaces a plan for human approval before dispatching sub-agents,” which involves a formal handoff.  
     - The contradiction is that 13 implies current plans fail to include approval steps, while 14 and 15 argue that such steps are necessary and effective. Yet, findings 5 and 6 (about Claude Code’s Plan Mode being read-only and requiring approval) appear to endorse the “approval gate” model (15), which stands in contrast to the problem in 13 — that plans often skip such steps. Thus, the conflict is between a real-world risk of missing approvals (13) and the claims that Claude Code’s mode includes them (5, 6), making the effectiveness of such gates questionable in practice.

4. **Model Delegation and Multi-Agent Functionality (Findings 3 vs. 10 vs. 9)**  
   - **Conflict**: Disagreement on whether the planning and execution stages use different models, and whether the division leads to better outcomes.  
   - **Detail**:  
     - Finding 3 claims Plan Mode uses Claude 3 Opus for planning and Claude 3 Haiku for execution, leading to improved performance (faster code generation, less debugging, less rework).  
     - Finding 10 (about Aider’s architect mode) similarly describes a separation: the “architect” explains changes, and a “separate editor” applies them — a clear two-model division.  
     - However, Finding 9 says Aider uses GPT-5 as the architect and a “cheaper editor” (presumably a smaller model), suggesting a trade-off in cost vs. capability.  
     - The contradiction arises in expectations: Finding 3 presents the dual-model approach as a *success story*, while Finding 10 and 9 suggest it’s only “worth it” when the expensive model (GPT-5 or Opus) is used, implying inefficiency or wasted cost unless the expensive model is hired — yet Finding 3 optimizes by using Haiku (cheaper) for execution. This raises a conflict in cost-performance trade-off assumptions — is the expensive architect always justified?

In summary, the main contradictions are in:
- Activation methods and user interaction with the plan (Findings 4, 5, 6),
- The scope of agent permissions (Findings 2, 5, 6 vs. 3),
- The necessity and implementation of human approval (Findings 13, 14, 15 vs. 5, 6),
- And the effectiveness vs. cost of model delegation (Findings 3, 9, 10).

These inconsistencies suggest that claims about process stability, safety, and performance in AI coding tools are not uniformly supported across sources and may depend on specific system implementations or evolving features.

## Gap Analysis
Here are the **critical gaps in evidence** — areas that the research *did NOT find* — that are highly relevant to Cercano’s intent of designing a next-generation holistic planning mode and a separate plan-execution skill. These gaps represent missing perspectives, underrepresented areas, absent data types, or unexplored dimensions in the existing literature and tools:

- **Lack of standardized, human-centric plan artifact formats** — While multiple tools (Claude Code, Aider) generate structured plans, no study describes a *unified, extensible, and semantically rich format* for the plan artifact that supports granularity, phase/task hierarchy, versioning, traceability, and interoperation across planning and execution systems. There is no evidence of *how to design a plan as a reusable, machine- and human-readable artifact*, especially one that supports dynamic extension.

- **Absence of longitudinal feedback mechanisms between execution and planning** — The research shows *plan-to-execution* handoffs, but there is no evidence on *how execution reports back to the planning system*, especially regarding:  
  - Unexpected deviations from the plan  
  - Runtime conditions that invalidate prior assumptions  
  - Runtime discoveries (e.g., emerging constraints, data anomalies) that trigger *plan re-generation or extension*  
  - Processes for *mid-run plan reopening* based on feedback loops (e.g., "revisit the architecture after testing the auth module").

- **No empirical exploration of planning in multi-stakeholder, cross-functional contexts** — All examples are technical/developmental (e.g., code refactoring, full-stack app). There is *no data* on how planning modes handle:  
  - Input from non-technical stakeholders (e.g., product, UX, legal)  
  - Conflict resolution between multiple objectives (e.g., “fast” vs. “secure” vs. “compliant”)  
  - Evolving requirements from user feedback mid-execution  
  - Planning that inherently includes risk, compliance, or regulatory tracking.

- **Missing user control and adaptive workflow shifts** — While some tools allow human approval (e.g., Claude’s Plan Mode), there is *no evidence* on:  
  - Dynamic control over plan granularity (e.g., “allow me to drill down into this task” vs. “keep it high-level”)  
  - User-initiated “pause and re-evaluate” triggers during execution  
  - Adaptive transition between “plan mode” and “execution mode” based on context (e.g., confidence, novelty, risk), rather than rigid, pre-defined mode toggle.

- **No research on "plan versioning" and change tracking for collaborative planning** — While plans are generated and modified, there is *no exploration* of:  
  - Versioning of plans (like Git for code)  
  - Conflict resolution between multiple planners or revisions  
  - Diffing and audit trails of *plan changes*, especially after human or AI input  
  - Ownership and accountability in shared planning sessions.

- **Underrepresentation of per-iteration planning cost and trade-off analysis** — The research showcases performance gains (e.g., 57% less rework, 35% less debugging), but *no data exists* on:  
  - Cost (time, compute, energy) of the planning phase vs. execution  
  - How planning effectiveness degrades with complexity or ambiguity  
  - What constitutes a "good enough" plan under time or resource constraints  
  - How planners balance depth vs. speed across different project domains.

- **Lack of evaluation on plan reusability and transfer across domains or projects** — There is *no data* on whether:  
  - Plans generated for one system (e.g., a web app) can be adapted or reused for a similar system (e.g., mobile app)  
  - Planning artifacts serve as a knowledge base or “playbook” for future projects  
  - Planners learn from past plans to improve future ones (e.g., via retrieval-augmented planning)

- **No exploration of human-AI co-planning with intentionality and explanation** — While some tools allow human edits (e.g., “edit in VS Code”), there is *no research* on:  
  - How to make the *AI’s plan reasoning explainable to the human* (e.g., why this module was split)  
  - How humans can *intervene to steer the plan structure*, not just edit output  
  - The role of “why” and “how” queries during plan refinement — and how the AI responds

- **Missing consideration of operationalization as a continuous loop** — The materials treat planning and execution as sequential phases. There is *no evidence* of systems designed around:  
  - A *continuous planning loop*, where each execution outcome triggers a reassessment and refinement of the plan  
  - Self-correcting or adaptive planning over extended time horizons  
  - Circularity in decision-making (e.g., "check if this plan is still valid after 48 hours of execution")

- **No evidence on adversarial consistency and robustness of plan artifacts** — The research assumes plans are truthful and stable, but there’s *no data* on:  
  - How plan artifacts withstand manipulation, hallucination, or bias in representation  
  - Tools or techniques to *validate the logical consistency* of plans (e.g., “does this task dependency graph form a DAG?”)  
  - Detection of subtle errors (e.g., missing edge cases, invalid dependencies) in the structured plan before execution

These gaps are not merely academic — they represent **design opportunities** that Cercano can leverage to build a truly *holistic*, *resilient*, and *human-in-the-loop* planning system that moves beyond current state-of-the-art.

## Recommended Reading Order
1. **"Exec Plan - AI-First SSOT"** — Establishes foundational context on the structure and integrity of plans as a shared, source-of-truth (SSOT) artifact. It introduces critical design considerations like timestamp standards (RFC 3339), the risks of assuming agent autonomy without explicit gates, and the dangers of omitting approvals, roots, or constraints. This provides essential grounding in how a plan should be designed to be auditable, traceable, and trustworthy—setting the stage for evaluating more specific tools.
2. **"Human-in-the-Loop AI Agents: When Approval Gates Matter"** — Builds directly on the foundation of SSOT by addressing *how* human oversight is embedded into the workflow. It validates the necessity of approval gates *within* the execution layer, minimizing context switching and preserving runtime state. This is crucial for Cercano’s goal of ensuring safety and control during plan execution, especially as plans transition from generation to action.
3. **"Agent-to-Human Handoff Patterns: Designing Escalation That Doesn't Break"** — Positions the human as the orchestrator, not just a passive reviewer. It explains how to design seamless handoffs: structuring the plan for clear delegation, using the orchestrator to funnel sub-agent outputs back into a unified narrative, and avoiding direct human exposure to raw agent outputs. This models the ideal feedback loop Cercano wants: a plan that can be *reviewed*, *approved*, *executed*, and *reopened* mid-run.
4. **"Aider's Architect Mode: Plan Before You Code"** — Introduces a peer-to-peer benchmark in plan-first design. Demonstrates how to separate high-level planning (architect) from implementation (editor), with clear evidence of improved outcomes: better modularity, fewer errors, and easier review. Highlights the power of propositional clarity before code edits.
5. **"Aider's architect mode: when the slower, more expensive workflow is worth it"** — Reinforces the architectural wisdom in Aider’s approach. Shows that a dedicated "plan" phase—where one model (or role) explains changes *once*, before any file is touched—leads to more coherent, accurate, and maintainable results. This directly supports Cercano’s goal of extracting a best-in-class *interactive brainstorm-to-converge* dialogue pattern.
6. **"Coding Tool Reviews: Aider's Architect Mode and Benchmarks"** — Provides empirical validation of the plan-first model. Offers real-world metrics: 14 functional groups identified in a Rust file via semantic analysis, 47 migration recommendations with 94.3% accuracy via fine-tuned LLM, and clear outcomes of modularization. This shows *how* a structured, hierarchical plan can be created and validated in practice.
7. **"How to Use Aider in 2026: Setup, Architect Mode & Git Wor......"** — Offers integration-level details: when to use GPT-5 as architect vs. a cheaper model as editor, and why the trade-off in cost vs. reasoning capacity is strategic. This informs Cercano’s decision-making on *which* models to use in *which* phase, and how to balance performance versus cost across the planning and execution lifecycle.
8. **"32 Claude Code Tricks That Actually Change How You..."** — Elevates the analysis to a *dominant* benchmark in performance and safety. Shows that separating planning (Claude 3 Opus) from execution (Claude 3 Haiku) yields: 92% task accuracy, 40% faster code generation, 35% less debugging, 57% less code rework. This provides the strongest evidence that *Cercano's dual-mode design—plan + execute—is not just sound, but superior in real-world outcomes.*
9. **"Claude Code Plan Mode: Design Review-First..."** — Shifts from performance to *process*. Details how Claude Code leverages subagents (Explore) to read the codebase without touching it, and how it delegates research to a restricted read-only subagent. This mirrors best practice in agent safety and ensures the planning phase is grounded in data without risk.
10. **"Plan Mode in Claude Code - Think Before You..."** — Describes how the final plan is *captured and edited*. It shows that the plan is exported to a user-selected text editor (VS Code, Vim, Notepad++), enabling full control and manual refinement. This reveals the importance of treating the plan as a *document*, not just an output—making it reusable, reusable, and composable.
11. **"Claude Code Plan Mode: Design Review-First..."** — Confirms the *gating mechanism*: plans are generated once and reviewed manually before any changes occur. This aligns with Cercano’s goal of ensuring user approval is required before execution, preserving the safety and control loop.
12. **"What is Plan Mode in Claude Code"** — Definitively establishes the *semantics* of Plan Mode: entirely read-only, no modification allowed, user approval required. This reconfirms the safety-by-design principles Cercano should emulate, especially since no code, file, or runtime is altered in the planning phase.
13. **"Chat modes | aider"** — Offers a contrast: in Aider, the interaction is command-based and more conversational. While useful, it lacks the *structured artifact* focus and true separation of thinking and doing. Acts as a cautionary note: tools that skip the plan artifact (e.g., `.md`, `.json`, `.yaml`) may not support the holistic, reusable, and extensible wanted by Cercano.
14. **"How to Use Claude Code as a Product Manager [2026]"** — Demonstrates concise access: `Shift+Tab` twice to enter Plan Mode, or CLI `claude --permission-mode plan`. Highlights the importance of discoverability and frictionless entry into the planning mode, supporting the intent to make this a natural, everyday workflow.

## Suggested Follow-Up Research
**"What are the design principles and semantic metadata structures for human- and machine-readable, version-controlled plan artifacts in collaborative AI planning systems?"** — *Why:* This directly addresses the lack of standardized, extensible plan formats. By investigating how successful systems encode granularity, hierarchy, traceability, and versioning (e.g., inspired by Git or ontologies), Cercano can establish a robust foundation for a reusable, auditable, and interoperable plan artifact that supports both AI and human interaction across planning and execution.
**"How do real-world AI-augmented teams manage mid-execution feedback loops from execution back to planning, especially when unexpected constraints or discoveries emerge?"** — *Why:* This targets the critical absence of longitudinal feedback mechanisms. Exploring case studies from domains like robotics, software deployment, or product development will reveal how humans and systems *re-open*, *adjust*, or *re-generate* plans during execution, enabling Cercano to design a dynamic, self-correcting planning loop that treats execution as input to planning, not just a terminal phase.
**"What role does inter-stakeholder conflict resolution play in AI-assisted planning for cross-functional teams, and how can such systems support non-technical input (e.g., UX, legal, product) in shaping the plan structure?"** — *Why:* This tackles the underrepresentation of multi-stakeholder planning. By examining how decisions are negotiated in mixed-competency teams, Cercano can develop a planning mode that integrates value trade-offs, handles conflicting objectives (e.g., speed vs. compliance), and allows non-technical users to contribute structurally — not just reactively — to the plan.
**"How do users control granularity and shift between planning and execution modes dynamically, and what heuristics do they use to trigger a ‘pause and re-evaluate’ mid-task?"** — *Why:* This addresses the missing research on user control and adaptive workflows. Investigating how people naturally adjust their planning depth or context-switch between modes would reveal patterns for building a system that *responds* to user signals (e.g., confidence, novelty, risk) rather than forcing rigid mode changes, enabling a fluid, intention-driven planning-execution continuum.
**"To what extent can AI-generated plans be validated for logical consistency (e.g., acyclicity, completeness, dependency correctness), and what tools or evaluation metrics exist for detecting hallucinated or malformed plan artifacts?"** — *Why:* This confronts the gap in adversarial robustness and quality assurance of plan artifacts. By identifying techniques to validate plan integrity — such as dependency graph analysis, constraint checking, or explainability audits — Cercano can engineer safeguards that ensure plans are not just generated, but *verified* before execution, reducing risk and increasing trust in the system’s output.

