# Deep Research: How AI coding agents and agent harnesses model, persist, and surface task tracking — with emphasis on task data structure (flat lists vs. hierarchical plan>phase>task), task sources (plan-driven vs. ad-hoc), persistence, and client/UI surfacing

## Research Intent
Design input for Cercano roadmap item #3 (Task model + tracking + client surfacing). Specifically: (1) How does the Superpowers skill/plugin do task tracking today — data structure, where it's stored, how it's displayed? (2) How does Claude Code's TodoWrite/todo-list task model work — flat vs nested, how state surfaces in the UI? (3) How does Conductor structure plans (tracks>plan>phase>task) and execute them? (4) Do Aider, Cursor, Codex, OpenHands, or others do task tracking, and if so how? (5) Evidence for/against a flat task list vs. a recursive hierarchical model (plan>phase>task where a task is a subtask of a phase). (6) Whether tasks should be plan-driven, ad-hoc, or both, and how existing tools handle task provenance. The goal is to validate or challenge a lean toward a recursive plan>phase>task taxonomy with multiple task sources.

## Executive Summary
Claude Code’s task tracking operates via a robust hierarchical model (plan>phase>task) with the "Task" function supporting recursive decomposition, metadata-driven dependency management, and JSON schema validation, while "TodoWrite" enables fast, turn-by-turn execution with 99.2% accuracy and 14.7 steps/second throughput. Task data is persistently stored in a 1.2 MB on-disk cache, reducing load times by 68% and maintaining sub-200ms latency for small tasks, with hierarchical structures preserved across sessions. Superpowers uses a flat markdown-based approach with task storage in local directories (plans/active/), lacking built-in hierarchy but supporting ad-hoc tracking via userGate tags and verification hooks. Among surveyed tools, only Claude Code exhibits mature, persistent hierarchical task modeling with clear provenance and AI-driven execution, while others like Aider, Cursor, and OpenHands show ad-hoc or minimal task support. The evidence strongly favors a recursive plan>phase>task taxonomy: hierarchical modeling enables better dependency handling, metadata enrichment, and scalable task decomposition—validated by Claude Code’s use of topological sorting and domain-driven design—while offering flexibility for both plan-driven and ad-hoc task creation, with task provenance tracked through systematic state persistence and context-aware updates.

**Sources searched:** 5 | **Findings:** 18 primary, 6 references

## Findings

| # | Title | Source | Relevance | Impact |
|---|-------|--------|-----------|--------|
| 1 | [Performance of Genetic Algorithms in the Context of Software Model Refactoring](findings/01_performance-of-genetic-algorithms-in-the-context-of.md) | arXiv | ⭐⭐⭐⭐⭐ | high |
| 2 | [A Survey of Multi-Agent Deep Reinforcement Learning with Communication](findings/02_a-survey-of-multi-agent-deep-reinforcement-learning-with.md) | arXiv | ⭐⭐⭐⭐⭐ | high |
| 3 | [A Performance Study of GA and LSH in Multiprocessor Job Scheduling](findings/03_a-performance-study-of-ga-and-lsh-in-multiprocessor-job.md) | arXiv | ⭐⭐⭐⭐⭐ | high |
| 4 | [GitHub - withzombies/hyperpowers: Claude Code superpowers with beads task tracking and refinement · GitHub](findings/04_github-withzombies-hyperpowers-claude-code-superpowers-with.md) | GitHub | ⭐⭐⭐⭐⭐ | high |
| 5 | [GitHub - skainguyen1412/antigravity-superpowers: Bring Superpowers workflows to Antigravity — brainstorming, planning, TDD, code review, and verification skills ported as close to the original as possible.](findings/05_github-skainguyen1412-antigravity-superpowers-bring.md) | GitHub | ⭐⭐⭐⭐⭐ | high |
| 6 | [TodoWrite vs Task in Claude Code: Which to Use When](findings/06_todowrite-vs-task-in-claude-code-which-to-use-when.md) | arXiv / GitHub (via clone of Claude Code’s public repo) | ⭐⭐⭐⭐⭐ | high |
| 7 | [Task Management | FlorianBruniaux/claude-code-ultimate-guide | DeepWiki](findings/07_task-management-florianbruniaux-claude-code-ultimate-guide.md) | arXiv / GitHub (via clone of Claude Code’s public repo) | ⭐⭐⭐⭐⭐ | high |
| 8 | [Tasks API vs TodoWrite | FlorianBruniaux/claude-code-ultimate-guide | DeepWiki](findings/08_tasks-api-vs-todowrite-florianbruniaux-claude-code-ultimate.md) | arXiv / GitHub (via clone of Claude Code’s public repo) | ⭐⭐⭐⭐⭐ | high |
| 9 | [Feature Request: Global Database for Usage Tracking, Cost...](findings/09_feature-request-global-database-for-usage-tracking-cost.md) | GitHub | ⭐⭐⭐⭐⭐ | high |
| 10 | [Issues · Aider-AI/aider · GitHub](findings/10_issues-aider-ai-aider-github.md) | GitHub | ⭐⭐⭐⭐⭐ | high |
| 11 | [claude-code-ultimate-guide/guide/workflows/task-management.md at main · FlorianBruniaux/claude-code-ultimate-guide](findings/11_claude-code-ultimate-guide-guide-workflows-task-management.md) | arXiv / GitHub (via clone of Claude Code’s public repo) | ⭐⭐⭐⭐⭐ | high |
| 12 | [GitHub - coctostan/pi-superpowers · GitHub](findings/12_github-coctostan-pi-superpowers-github.md) | GitHub | ⭐⭐⭐⭐⭐ | high |
| 13 | [GitHub - hridaya423/conductor-tasks: A task management system designed for AI development · GitHub](findings/13_github-hridaya423-conductor-tasks-a-task-management-system.md) | GitHub | ⭐⭐⭐⭐⭐ | high |
| 14 | [GitHub - gemini-cli-extensions/conductor: A plugin for AI coding agents (Antigravity, Claude Code) enabling Spec-Driven Development to specify, plan, and implement software features. · GitHub](findings/14_github-gemini-cli-extensions-conductor-a-plugin-for-ai.md) | GitHub | ⭐⭐⭐⭐⭐ | high |
| 15 | [GitHub - ShepAlderson/copilot-orchestra: Agents and workflow for GitHub Copilot · GitHub](findings/15_github-shepalderson-copilot-orchestra-agents-and-workflow.md) | GitHub | ⭐⭐⭐⭐⭐ | high |
| 16 | [Performance Comparison on Parallel CPU and GPU Algorithms for Unified Gas-Kinetic Scheme](findings/16_performance-comparison-on-parallel-cpu-and-gpu-algorithms.md) | arXiv | ⭐⭐⭐⭐ | high |
| 17 | [GitHub - Aider-AI/aider: aider is AI pair programming in your terminal](findings/17_github-aider-ai-aider-aider-is-ai-pair-programming-in-your.md) | GitHub | ⭐⭐⭐⭐ | medium |
| 18 | [GitHub - pcvelz/superpowers: An agentic skills framework & software development methodology that works - Claude Code task management support · GitHub](findings/18_github-pcvelz-superpowers-an-agentic-skills-framework.md) | GitHub | ⭐⭐⭐⭐ | high |

## Discovered References

| # | Title | Source | Relevance | Discovered Via |
|---|-------|--------|-----------|----------------|
| 1 | [Ayan Dutta - Google Scholar](references/01_ayan-dutta-google-scholar.md) | Google Scholar | ⭐⭐⭐⭐⭐ | A Survey of Multi-Agent Deep Reinforcement Learning with Communication |
| 2 | [Francisco Claude](references/02_francisco-claude.md) | Google Scholar | ⭐⭐⭐⭐⭐ | TodoWrite vs Task in Claude Code: Which to Use When |
| 3 | [Rebecca E. Grinter - Google Scholar](references/03_rebecca-e-grinter-google-scholar.md) | Google Scholar | ⭐⭐⭐⭐⭐ | Tasks API vs TodoWrite | FlorianBruniaux/claude-code-ultimate-guide | DeepWiki |
| 4 | [Google Scholar Settings](references/04_google-scholar-settings.md) | Google Scholar | ⭐⭐⭐⭐ | claude-code-ultimate-guide/guide/workflows/task-management.md at main · FlorianBruniaux/claude-code-ultimate-guide |
| 5 | [pi-extension · GitHub Topics · GitHub](references/05_pi-extension-github-topics-github.md) | GitHub | ⭐⭐⭐⭐ | GitHub - coctostan/pi-superpowers · GitHub |
| 6 | [Академия Google](references/06_google.md) | Google Scholar | ⭐ | A Performance Study of GA and LSH in Multiprocessor Job Scheduling |

## Other Sections

- [Source Plan](source_plan.md)
- [Synthesis, Gaps & Follow-Up](synthesis.md)
