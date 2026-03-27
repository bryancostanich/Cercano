# Track Plan: Deep Research Skill

## Phase 1: Source Planning Engine

### Objective
Build the logic that analyzes a topic + intent and produces a ranked list of sources with tailored search queries.

### Tasks
- [x] Task: Create `internal/research/` package with source planning.
    - [x] Define `Source` struct: Name, Type (api/web), Site, Queries []string, Reason string.
    - [x] Define `ResearchPlan` struct: Topic, Intent, Sources []Source.
    - [x] Implement `PlanSources(ctx, model, topic, intent, depth, dateRange)` — prompts local model.
    - [x] Build source registry: 25+ sources across academic, industry, news, reference, regulatory.
    - [x] Red/Green TDD: TestParsePlanResponse, TestPlanSources_FallbackOnEmpty, TestSourceRegistry_HasSources, TestFindSource_Known, TestFindSource_CaseInsensitive.
- [x] Task: Implement source override support.
    - [x] `PlanWithOverride` uses user-specified sources with model-generated queries.
    - [x] Red/Green TDD: TestPlanWithOverride.

## Phase 2: Academic API Clients

### Objective
Build lightweight clients for free academic APIs.

### Tasks
- [x] Task: Implement PubMed client.
    - [x] `searchPubMed` using NCBI E-utilities (esearch + esummary).
    - [x] Rate limiting: 350ms between requests.
- [x] Task: Implement arXiv client.
    - [x] `searchArXiv` using Atom XML API.
- [x] Task: Define unified `Publication` struct.
    - [x] Deduplication by URL.
    - [x] Red/Green TDD: TestDeduplicatePubs, TestSearchPubMed_ParsesResults, TestSearchArXiv_ParsesAtomXML.

## Phase 3: Web-Scoped Search

### Objective
For sources without direct APIs, use DuckDuckGo with site-scoped queries.

### Tasks
- [x] Task: Implement site-scoped DuckDuckGo search.
    - [x] `searchWeb` prepends `site:domain.com` to queries.
    - [x] Red/Green TDD: TestSearchWeb_SiteScoped.
- [x] Task: Implement unified search dispatcher.
    - [x] `SearchDispatcher` routes to PubMed, arXiv, or web based on source type.
    - [x] `SearchAllSources` searches concurrently.

## Phase 4: Content Extraction & Analysis

### Objective
Fetch content for top results and analyze each with the local model.

### Tasks
- [x] Task: Implement per-finding analysis with rich summaries.
    - [x] `AnalyzeFinding` with section-aware multi-line parser.
    - [x] Summary, KeyFindings (bullet points), WhyItMatters, HowToUse, RelevanceScore, ImpactRating, CitedRefs.
    - [x] Prompt demands concrete facts, numbers, methods, conclusions — not vague descriptions.
    - [x] Red/Green TDD: TestAnalyzeFinding_ParsesAnnotation, TestAnalyzeFinding_FallbackSummary, TestParseCitedRef.
- [x] Task: Implement reference chasing.
    - [x] Extracts cited references from findings, deduplicates, searches, analyzes.
    - [x] 1-hop depth limit. Budget: max 5 per finding, max 50 total (thorough) / 10 total (survey).
    - [x] Red/Green TDD: TestChaseReferences_RespectsDepthLimit, TestChaseReferences_DeduplicatesExisting.
- [x] Task: Implement batch analysis.
    - [x] `AnalyzeAll` processes sequentially with graceful degradation.
    - [x] Red/Green TDD: TestAnalyzeAll_SkipsEmptyContent.

## Phase 5: Synthesis & Report Compilation

### Objective
Sort, synthesize, and compile findings into the final structured report.

### Tasks
- [x] Task: Implement executive summary, synthesis, contradictions, gap analysis, follow-up queries, reading order.
    - [x] All via local model prompts.
- [x] Task: Implement multi-file report output.
    - [x] `WriteReport` creates directory structure: README.md, findings/, references/, source_plan.md, synthesis.md.
    - [x] Individual finding files with full detail.
    - [x] README with table of contents linking to finding files.
    - [x] Red/Green TDD: TestCompileReport_SortsByRelevance, TestCompileReport_IncludesAllSections, TestCompileReport_SeparatesChasedFindings.
- [x] Task: Single-file fallback via `CompileReport` when no output_dir.

## Phase 5b: Progress Persistence

### Objective
Save intermediate results to disk so crashes don't lose work.

### Tasks
- [x] Task: Implement checkpoint system.
    - [x] `.cercano/research/<topic-hash>/` with plan.json, search_results.json, findings.json, sections.json.
    - [x] Resume from last checkpoint on retry.
    - [x] Cleanup after completion (keep if output_dir set).
    - [x] Red/Green TDD: TestCheckpoint_SaveAndLoad, TestCheckpoint_HasPhase, TestCheckpoint_DeterministicHash, TestCheckpoint_Cleanup, TestCheckpoint_SaveAndLoadAllTypes.

## Phase 6: MCP Tool Handler & Integration

### Objective
Wire everything together as a `cercano_deep_research` MCP tool.

### Tasks
- [x] Task: Add `DeepResearchRequest` struct and register tool.
    - [x] Parameters: topic, intent, depth, date_range, sources, output_dir, project_dir.
- [x] Task: Implement `handleDeepResearch` handler.
    - [x] Adapter pattern: webSearchAdapter, webFetchAdapter bridge web package to research interfaces.
    - [x] output_dir: write multi-file report, return summary.
    - [x] Inline: return full compiled report.
    - [x] Telemetry with content_tokens_avoided.
- [x] Task: Add to `builtinSkills()`.
- [x] Task: Create `.agents/skills/cercano-deep-research/SKILL.md`.
- [ ] Task: Conductor - User Manual Verification 'MCP Tool Handler & Integration' (Protocol in workflow.md)

## Phase 7: Documentation & Polish

### Objective
Update project docs and polish the output format.

### Tasks
- [x] Task: Update README with `cercano_deep_research` tool description.
- [ ] Task: Conductor - User Manual Verification 'Documentation & Polish' (Protocol in workflow.md)
