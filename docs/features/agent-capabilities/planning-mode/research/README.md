# Deep Research: Planning modes and brainstorming/plan-capture workflows in AI coding agents: Superpowers (obra/superpowers) brainstorming + planning skills, Claude Code plan mode, Gemini CLI Conductor spec-driven planning, Aider architect mode, Cursor/Windsurf planning, OpenHands, and any other notable agent planning systems

## Research Intent
Cercano is designing a holistic planning mode (interactive generation + structured plan capture) plus a separate plan-execution skill. We want to identify which existing planning modes are best-in-class and extract the best design ideas: how they run the interactive brainstorm-to-converge dialogue, how they capture the plan as a structured artifact (format, granularity, phase/task hierarchy), how they gate approval, how they hand plans to execution, and how execution reports back and can reopen/extend the plan mid-run.

## Executive Summary
Cercano’s research identifies Claude Code’s Plan Mode as a leading exemplar for holistic planning, showcasing a robust separation of exploration and execution: it uses a high-level reasoning model (Claude 3 Opus) for in-depth planning and a specialized execution model (Claude 3 Haiku) for fast, precise implementation, reducing code rework by 57% and cutting debugging time by 35%. The process is inherently safe, operating in read-only mode with user approval required before any changes, and enables structured plan capture via editable text files in external editors like VS Code or Vim, supporting a natural handoff to execution. A key strength lies in its subagent-driven research—using a dedicated, read-only Explore subagent to analyze codebases—ensuring deep context awareness without risk of unintended modifications. This dual-phase, human-in-the-loop design—with clear gateways for approval, structured artifact output, and seamless path to execution and feedback—provides a compelling blueprint for Cercano's own planning and execution systems.

**Sources searched:** 5 | **Findings:** 29 primary, 4 references

## Findings

| # | Title | Source | Relevance | Impact |
|---|-------|--------|-----------|--------|
| 1 | [Agent-to-Human Handoff Patterns: Designing Escalation That Doesn't Break | Zylos Research](findings/01_agent-to-human-handoff-patterns-designing-escalation-that.md) | SSRN (Social sciences, economics, law) + Semantic Scholar (Cross-discipline, citation graph) | ⭐⭐⭐⭐⭐ | high |
| 2 | [Getting Started with Conductor for Gemini CLI - KDnuggets](findings/02_getting-started-with-conductor-for-gemini-cli-kdnuggets.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 3 | [32 Claude Code Tricks That Actually Change How You... | MindStudio](findings/03_32-claude-code-tricks-that-actually-change-how-you.md) | arXiv (CS preprints) | ⭐⭐⭐⭐⭐ | high |
| 4 | [Plan Mode in Claude Code - Think Before You... - codewithmukesh](findings/04_plan-mode-in-claude-code-think-before-you-codewithmukesh.md) | arXiv (CS preprints) | ⭐⭐⭐⭐⭐ | high |
| 5 | [How to Use Claude Code as a Product Manager [2026]](findings/05_how-to-use-claude-code-as-a-product-manager-2026.md) | arXiv (CS preprints) | ⭐⭐⭐⭐⭐ | high |
| 6 | [What is Plan Mode in Claude Code | ClaudeLog](findings/06_what-is-plan-mode-in-claude-code-claudelog.md) | arXiv (CS preprints) | ⭐⭐⭐⭐⭐ | high |
| 7 | [Planner-Executor-Evaluator Loop Architecture](findings/07_planner-executor-evaluator-loop-architecture.md) | arXiv (CS preprints) + GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 8 | [How to Use Aider in 2026: Setup, Architect Mode & Git Wor......](findings/08_how-to-use-aider-in-2026-setup-architect-mode-git-wor.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 9 | [Coding Tool Reviews: Aider's Architect Mode and Benchmarks | PorkiCoder Blog](findings/09_coding-tool-reviews-aider-s-architect-mode-and-benchmarks.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 10 | [Aider's architect mode: when the slower, more expensive workflow is worth it | Tinker AI](findings/10_aider-s-architect-mode-when-the-slower-more-expensive.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 11 | [Step-by-Step Guide: Refactoring a Large Rust Codebase with aider.dev and Custom LLMs](findings/11_step-by-step-guide-refactoring-a-large-rust-codebase-with.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 12 | [Aider's Architect Mode: Plan Before You Code | AI Stack Today | AI Stack Today](findings/12_aider-s-architect-mode-plan-before-you-code-ai-stack-today.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 13 | [Exec Plan - AI-First SSOT](findings/13_exec-plan-ai-first-ssot.md) | SSRN (Social sciences, economics, law) + Semantic Scholar (Cross-discipline, citation graph) | ⭐⭐⭐⭐⭐ | high |
| 14 | [Human-in-the-Loop AI Agents: When Approval Gates Matter](findings/14_human-in-the-loop-ai-agents-when-approval-gates-matter.md) | SSRN (Social sciences, economics, law) + Semantic Scholar (Cross-discipline, citation graph) | ⭐⭐⭐⭐⭐ | high |
| 15 | [Claude Code Plan Mode: Design Review-First... | DataCamp](findings/15_claude-code-plan-mode-design-review-first-datacamp.md) | arXiv (CS preprints) | ⭐⭐⭐⭐⭐ | high |
| 16 | [Building AI Agent Workflows with Branching and Approval Gates | AgentC2](findings/16_building-ai-agent-workflows-with-branching-and-approval.md) | SSRN (Social sciences, economics, law) + Semantic Scholar (Cross-discipline, citation graph) | ⭐⭐⭐⭐⭐ | high |
| 17 | [claudearchitectcertification.com/concepts/plan-mode.md](findings/17_claudearchitectcertification-com-concepts-plan-mode-md.md) | arXiv (CS preprints) | ⭐⭐⭐⭐⭐ | high |
| 18 | [Authorization and Governance for AI Agents: Runtime Authorization Beyond Identity at Scale | Microsoft Community Hub](findings/18_authorization-and-governance-for-ai-agents-runtime.md) | SSRN (Social sciences, economics, law) + Semantic Scholar (Cross-discipline, citation graph) | ⭐⭐⭐⭐⭐ | high |
| 19 | [GitHub - gemini-cli-extensions/conductor: A plugin for AI coding agents (Antigravity, Claude Code) enabling Spec-Driven Development to specify, plan, and implement software features. · GitHub](findings/19_github-gemini-cli-extensions-conductor-a-plugin-for-ai.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 20 | [Human-in-the-Loop AI Agents: When Approval Gates Matter](findings/20_human-in-the-loop-ai-agents-when-approval-gates-matter.md) | SSRN (Social sciences, economics, law) + Semantic Scholar (Cross-discipline, citation graph) | ⭐⭐⭐⭐⭐ | high |
| 21 | [Gemini CLI Plan Mode Separates Thinking From Doing — and Makes Read-Only the Default - DevOps.com](findings/21_gemini-cli-plan-mode-separates-thinking-from-doing-and.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 22 | [Introducing context-driven development for Gemini CLI | TechLife](findings/22_introducing-context-driven-development-for-gemini-cli.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 23 | [Spec-Driven Development with Gemini CLI | by Giovanni Galloro | Google Cloud - Community | Medium](findings/23_spec-driven-development-with-gemini-cli-by-giovanni-galloro.md) | GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 24 | [Feature: Introduce an Ask/Plan mode and allow easy switch an agent in context between two modes · Issue #557 · OpenHands/software-agent-sdk](findings/24_feature-introduce-an-ask-plan-mode-and-allow-easy-switch-an.md) | arXiv (CS preprints) + GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 25 | [The OpenHands Software Agent SDK: A Composable and Extensible Foundation for Production Agents](findings/25_the-openhands-software-agent-sdk-a-composable-and.md) | arXiv (CS preprints) + GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 26 | [🙌 OpenHands — Deep Dive & Build-Your-Own Guide 📚 - DEV Community](findings/26_openhands-deep-dive-build-your-own-guide-dev-community.md) | arXiv (CS preprints) + GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 27 | [GitHub - OpenHands/OpenHands: 🙌 OpenHands: AI-Driven Development](findings/27_github-openhands-openhands-openhands-ai-driven-development.md) | arXiv (CS preprints) + GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 28 | [[PRD] Planning Agent · Issue #8964 · OpenHands/OpenHands](findings/28_prd-planning-agent-issue-8964-openhands-openhands.md) | arXiv (CS preprints) + GitHub (Open source implementations) | ⭐⭐⭐⭐⭐ | high |
| 29 | [Chat modes | aider](findings/29_chat-modes-aider.md) | GitHub (Open source implementations) | ⭐⭐ | low |

## Discovered References

| # | Title | Source | Relevance | Discovered Via |
|---|-------|--------|-----------|----------------|
| 1 | [Neel Nanda - Google Scholar](references/01_neel-nanda-google-scholar.md) | Google Scholar | ⭐⭐⭐⭐⭐ | 🙌 OpenHands — Deep Dive & Build-Your-Own Guide 📚 - DEV Community |
| 2 | [Hoang H. Tran - Google Scholar](references/02_hoang-h-tran-google-scholar.md) | Google Scholar | ⭐⭐⭐⭐⭐ | GitHub - OpenHands/OpenHands: 🙌 OpenHands: AI-Driven Development |
| 3 | [Junyu Liu - Google Scholar](references/03_junyu-liu-google-scholar.md) | Google Scholar | ⭐⭐⭐⭐ | Claude Code Plan Mode: Design Review-First... | DataCamp |
| 4 | [Академия Google](references/04_google.md) | Google Scholar | ⭐⭐⭐⭐ | How to Use Aider in 2026: Setup, Architect Mode & Git Wor...... |

## Other Sections

- [Source Plan](source_plan.md)
- [Synthesis, Gaps & Follow-Up](synthesis.md)
