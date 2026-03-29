# Track Plan: Deep Research Enhancement

## Phase 0: Model Check & Auto-Switch

### Objective
Detect when the active model is a poor fit for research tasks and offer to switch to a better one. Applies to both `cercano_research` and `cercano_deep_research`.

### Tasks
- [ ] Task: Define research-capable model list.
    - [ ] Maintain a list of models known to be good for research/analysis (general-purpose, reasoning-focused) vs code-only.
    - [ ] Code-only models to flag: qwen3-coder, codellama, deepseek-coder, starcoder, etc.
    - [ ] Research-capable alternatives: qwen2.5, llama3.1, gemma2, deepseek-r1, mistral, command-r, etc.
    - [ ] Red/Green TDD: TestIsCodeOnlyModel, TestSuggestResearchModel.
- [ ] Task: Implement model check at research handler entry.
    - [ ] `CheckResearchModel(currentModel string, availableModels []string) (suggestion string, shouldSwitch bool)`.
    - [ ] Query available models from Ollama via the existing ListModels gRPC call.
    - [ ] If current model is code-only AND a research-capable model is available, return a suggestion.
    - [ ] If no better model is available, proceed with current model (don't block).
    - [ ] Red/Green TDD: TestCheckResearchModel_SuggestsSwitch, TestCheckResearchModel_NoBetterAvailable.
- [ ] Task: Add model suggestion to research tool responses.
    - [ ] If a better model is available, prepend a note to the response: "Note: You're using [coder model] which is optimized for code, not research. For better results, switch with: cercano_config(action: 'set', local_model: '[suggested]')"
    - [ ] Only suggest, never auto-switch (user controls their config).
    - [ ] Apply to both `handleResearch` and `handleDeepResearch`.
- [ ] Task: Add `research_model` field to config (optional).
    - [ ] If set, deep_research and research tools use this model instead of the default local_model.
    - [ ] Allows keeping qwen3-coder for code tasks and e.g. qwen2.5 for research.
    - [ ] Wire into the model caller for research handlers.
    - [ ] Red/Green TDD: TestResearchModel_UsedWhenSet, TestResearchModel_FallsBackToLocalModel.

## Phase 1: Multi-Pass Analysis Pipeline

### Objective
Replace the single overloaded analysis call with three focused passes that produce richer, more specific findings.

### Tasks
- [ ] Task: Implement Pass 1 — Fact Extraction.
    - [ ] New prompt: "Read this content and extract every concrete fact, number, method, result, and conclusion. Return a bullet list of facts only."
    - [ ] `ExtractFacts(ctx, model, pub Publication, content string) ([]string, error)`.
    - [ ] Simple bullet-list parsing. No structured formatting required — just facts.
    - [ ] Red/Green TDD: TestExtractFacts_ReturnsBullets, TestExtractFacts_EmptyContent.
- [ ] Task: Implement Pass 2 — Relevance Analysis.
    - [ ] New prompt: takes extracted facts + user intent, produces WHY_IT_MATTERS + HOW_TO_USE + RELEVANCE + IMPACT.
    - [ ] `AnalyzeRelevance(ctx, model, facts []string, title, intent, crossContext string) (*RelevanceResult, error)`.
    - [ ] Simpler prompt — model only does relevance analysis, not summarization.
    - [ ] Cross-finding context string passed in (see Phase 2).
    - [ ] Red/Green TDD: TestAnalyzeRelevance_ParsesScores, TestAnalyzeRelevance_WithCrossContext.
- [ ] Task: Implement Pass 3 — Critique & Refine (quality gate).
    - [ ] New prompt: reviews the combined summary + facts + relevance for specificity.
    - [ ] `CritiqueAndRefine(ctx, model, summary string, facts []string, intent string) (string, error)`.
    - [ ] Returns refined summary. If original is already good, returns it unchanged.
    - [ ] Red/Green TDD: TestCritiqueAndRefine_ImprovesVague, TestCritiqueAndRefine_KeepsGood.
- [ ] Task: Wire three passes into `AnalyzeFinding`.
    - [ ] Replace single-prompt analysis with: ExtractFacts → AnalyzeRelevance → build summary from facts → CritiqueAndRefine.
    - [ ] KeyFindings populated from Pass 1 facts.
    - [ ] Summary built from refined output.
    - [ ] Red/Green TDD: TestAnalyzeFinding_MultiPass_ProducesRicherOutput.

## Phase 2: Cross-Finding Context

### Objective
Give the model awareness of previously analyzed findings so it can draw connections and contrasts.

### Tasks
- [ ] Task: Build cross-finding context string.
    - [ ] `BuildCrossContext(findings []AnnotatedFinding) string` — 1-line summary per prior finding.
    - [ ] Format: "1. [Source] Title — key fact or conclusion"
    - [ ] Cap at 15 entries to keep context small.
    - [ ] Red/Green TDD: TestBuildCrossContext_FormatsCorrectly, TestBuildCrossContext_CapsAt15.
- [ ] Task: Pass cross-context to AnalyzeRelevance (Pass 2).
    - [ ] Add instruction: "How does this finding relate to, contrast with, or build on the previously analyzed findings?"
    - [ ] Model can now say "unlike X" or "corroborates finding #N".
- [ ] Task: Update `AnalyzeAll` to accumulate context as it processes findings.
    - [ ] After each finding is analyzed, append its 1-liner to the context.
    - [ ] Sequential processing is already in place — just accumulate.
    - [ ] Red/Green TDD: TestAnalyzeAll_PassesCrossContext.

## Phase 3: Quality Gate with Re-Prompting

### Objective
Detect vague or content-free analysis and force the model to be more specific.

### Tasks
- [ ] Task: Implement quality scorer.
    - [ ] `ScoreQuality(ctx, model, summary string, keyFindings []string) (bool, string, error)`.
    - [ ] Prompt asks: "Does this contain specific facts/numbers/methods? Or is it generic filler?"
    - [ ] Returns pass/fail + critique explaining what's vague.
    - [ ] Red/Green TDD: TestScoreQuality_PassesGoodSummary, TestScoreQuality_FailsVagueSummary.
- [ ] Task: Implement re-prompting on failure.
    - [ ] If quality check fails, re-run Pass 1 (fact extraction) with critique appended: "The previous analysis was too vague: [critique]. Extract MORE SPECIFIC facts."
    - [ ] Max 1 retry per finding to avoid loops.
    - [ ] Red/Green TDD: TestRePrompt_ImprovesOnRetry, TestRePrompt_MaxOneRetry.
- [ ] Task: Wire quality gate into `AnalyzeFinding` after Pass 3.
    - [ ] If CritiqueAndRefine produces a FAIL, retry fact extraction with critique.
    - [ ] Track retry count to enforce max 1.

## Phase 4: Depth Over Breadth

### Objective
Adjust defaults to produce fewer, deeper findings rather than many shallow ones.

### Tasks
- [ ] Task: Update `DefaultConfig` parameters.
    - [ ] Survey: MaxPrimaryResults 5→3 per source, AnalysisTruncate 8K→12K.
    - [ ] Thorough: MaxPrimaryResults 10→6 per source, AnalysisTruncate 10K→15K.
    - [ ] Net effect: fewer total findings, but each one gets 3-4 model calls instead of 1.
    - [ ] Red/Green TDD: TestDefaultConfig_NewValues.
- [ ] Task: Update source planning prompt.
    - [ ] Ask model to pick 3-4 sources (survey) or 5-6 (thorough) instead of 3-8.
    - [ ] Emphasis: "Choose the MOST relevant sources. Quality over quantity."

## Phase 5: Example-Driven Prompts

### Objective
Include concrete good-vs-bad examples in prompts to guide the model.

### Tasks
- [ ] Task: Add examples to fact extraction prompt.
    - [ ] Show: BAD (vague statement) vs GOOD (specific fact with number/method).
    - [ ] "BAD: 'The tool uses a novel approach.' GOOD: 'The tool uses 4-bit GPTQ quantization achieving 15 tok/s on M2 MacBook Air.'"
- [ ] Task: Add examples to relevance analysis prompt.
    - [ ] Show: BAD (generic "this is relevant") vs GOOD (specific connection to intent).
    - [ ] "BAD: 'This directly addresses the competitive landscape.' GOOD: 'ExecuTorch's 50KB footprint is 10x smaller than Cercano's current binary, suggesting a potential optimization target for embedded deployment.'"
- [ ] Task: Add examples to quality critique prompt.
    - [ ] Show what PASS vs FAIL looks like.
- [ ] Task: Validate with test runs.
    - [ ] Run same query before and after examples. Compare summary specificity.

## Phase 6: Integration & Validation

### Objective
Verify the enhanced pipeline produces measurably better output.

### Tasks
- [ ] Task: Run before/after comparison.
    - [ ] Same topic + intent, compare: summary length, fact count, cross-references, score distribution.
    - [ ] Document improvement in track notes.
- [ ] Task: Update SKILL.md with new behavior notes.
    - [ ] Note: multi-pass analysis, quality gating, increased latency.
- [ ] Task: Conductor - User Manual Verification 'Integration & Validation' (Protocol in workflow.md)
