# Track Plan: Deep Research Skill

## Phase 1: Source Planning Engine

### Objective
Build the logic that analyzes a topic + intent and produces a ranked list of sources with tailored search queries.

### Tasks
- [ ] Task: Create `internal/research/` package with source planning.
    - [ ] Define `Source` struct: Name, Type (api/web), BaseURL, Queries []string, Reason string.
    - [ ] Define `ResearchPlan` struct: Topic, Intent, Sources []Source.
    - [ ] Implement `PlanSources(ctx, grpcClient, topic, intent string) (*ResearchPlan, error)` — prompts local model to identify relevant sources and generate search queries.
    - [ ] Build source registry: known sources (PubMed, arXiv, bioRxiv, ClinicalTrials.gov, Google Scholar, IEEE, FDA, GitHub, Patents, General web) with their search mechanisms.
    - [ ] Red/Green TDD: TestPlanSources_ParsesModelOutput, TestSourceRegistry_KnownSources.
- [ ] Task: Implement source override support.
    - [ ] If user provides explicit `sources` parameter, use those instead of model-planned ones.
    - [ ] Still generate tailored queries per source via the model.
    - [ ] Red/Green TDD: TestPlanSources_UserOverride.

## Phase 2: Academic API Clients

### Objective
Build lightweight clients for free academic APIs (PubMed, arXiv, bioRxiv, ClinicalTrials.gov).

### Tasks
- [ ] Task: Implement PubMed client.
    - [ ] `SearchPubMed(ctx, query string, maxResults int) ([]Publication, error)` using NCBI E-utilities.
    - [ ] Parse JSON response: PMID, title, authors, journal, date, abstract.
    - [ ] Rate limiting: max 3 requests/sec.
    - [ ] Red/Green TDD: TestSearchPubMed_ParsesResults, TestSearchPubMed_RateLimit (with mock server).
- [ ] Task: Implement arXiv client.
    - [ ] `SearchArXiv(ctx, query string, maxResults int) ([]Publication, error)` using Atom API.
    - [ ] Parse XML response: arxiv ID, title, authors, abstract, PDF URL, categories.
    - [ ] Red/Green TDD: TestSearchArXiv_ParsesAtomXML.
- [ ] Task: Implement bioRxiv client.
    - [ ] `SearchBioRxiv(ctx, query string, maxResults int) ([]Publication, error)`.
    - [ ] Fallback to DuckDuckGo `site:biorxiv.org` if API doesn't match well.
    - [ ] Red/Green TDD: TestSearchBioRxiv_ParsesResults.
- [ ] Task: Implement ClinicalTrials.gov client.
    - [ ] `SearchClinicalTrials(ctx, query string, maxResults int) ([]Publication, error)` using v2 API.
    - [ ] Parse JSON: NCT ID, title, status, phase, conditions, interventions.
    - [ ] Red/Green TDD: TestSearchClinicalTrials_ParsesResults.
- [ ] Task: Define unified `Publication` struct.
    - [ ] Fields: Title, Authors, Source, URL, Date, Abstract, DOI, Metadata map[string]string.
    - [ ] Deduplication by DOI or URL.
    - [ ] Red/Green TDD: TestDeduplicatePublications.

## Phase 3: Web-Scoped Search

### Objective
For sources without direct APIs, use DuckDuckGo with site-scoped queries.

### Tasks
- [ ] Task: Implement site-scoped DuckDuckGo search.
    - [ ] `SearchSiteScoped(ctx, site, query string, maxResults int) ([]Publication, error)`.
    - [ ] Reuse existing `web.SearchDDG` with `site:domain.com` prefix.
    - [ ] Parse results into Publication structs.
    - [ ] Support: Google Scholar, IEEE Xplore, FDA.gov, GitHub, Patents.
    - [ ] Red/Green TDD: TestSearchSiteScoped_FormatsQuery.
- [ ] Task: Implement unified search dispatcher.
    - [ ] `ExecuteSearch(ctx, source Source) ([]Publication, error)` — routes to the right client based on source type.
    - [ ] API sources → dedicated client. Web sources → site-scoped DDG.
    - [ ] Red/Green TDD: TestExecuteSearch_RoutesToCorrectClient.

## Phase 4: Content Extraction & Analysis

### Objective
Fetch content for top results and analyze each with the local model.

### Tasks
- [ ] Task: Implement content fetcher for publications.
    - [ ] `FetchContent(ctx, pub Publication) (string, error)` — fetch URL, extract readable text.
    - [ ] Reuse `web.FetchURL`. For abstract-only pages, prefer the abstract field from API metadata.
    - [ ] Gracefully handle fetch failures (skip, note in report).
    - [ ] Red/Green TDD: TestFetchContent_UsesAbstractFallback, TestFetchContent_HandlesFailure.
- [ ] Task: Implement per-finding analysis.
    - [ ] `AnalyzeFinding(ctx, grpcClient, pub Publication, content, intent string) (*AnnotatedFinding, error)`.
    - [ ] AnnotatedFinding: Publication, Summary, WhyItMatters, HowToUse, RelevanceScore (1-5), ImpactRating (low/medium/high), CitedReferences []Reference.
    - [ ] Prompt the local model with the content + user's intent.
    - [ ] Parse structured output (relevance score, impact, annotations, extracted references).
    - [ ] Red/Green TDD: TestAnalyzeFinding_ParsesAnnotation, TestAnalyzeFinding_ScoredCorrectly, TestAnalyzeFinding_ExtractsReferences.
- [ ] Task: Implement reference chasing.
    - [ ] Collect cited references from all analyzed findings into a chase queue.
    - [ ] Deduplicate against existing findings (by title or URL).
    - [ ] For each queued reference: search by title on original source, fall back to DuckDuckGo.
    - [ ] Fetch, analyze, and annotate the same way as primary findings.
    - [ ] Mark as "Discovered via [Parent Finding]" in the report.
    - [ ] Depth limit: 1 hop. Budget: max 5 per finding, max 50 total (thorough) / 10 total (survey).
    - [ ] Red/Green TDD: TestReferenceChasing_DeduplicatesExisting, TestReferenceChasing_RespectsDepthLimit, TestReferenceChasing_MarksParent.
- [ ] Task: Implement batch analysis with progress tracking.
    - [ ] Process findings sequentially (to avoid overloading local model).
    - [ ] Track progress: "Analyzing finding 5 of 23..." / "Chasing reference 3 of 12..."
    - [ ] Red/Green TDD: TestBatchAnalysis_AllProcessed.

## Phase 5: Synthesis & Report Compilation

### Objective
Sort, synthesize, and compile findings into the final structured report with executive summary, contradiction detection, gap analysis, and follow-up suggestions.

### Tasks
- [ ] Task: Implement executive summary generator.
    - [ ] `GenerateExecutiveSummary(ctx, grpcClient, findings []AnnotatedFinding, intent string) (string, error)`.
    - [ ] Local model produces 3-4 sentence TL;DR.
    - [ ] Red/Green TDD: TestExecutiveSummary_Concise.
- [ ] Task: Implement narrative synthesis.
    - [ ] `Synthesize(ctx, grpcClient, findings []AnnotatedFinding, intent string) (string, error)`.
    - [ ] Local model generates 2-3 paragraph narrative tying findings together.
    - [ ] Highlights key themes and how they connect to the intent.
    - [ ] Red/Green TDD: TestSynthesize_ProducesNarrative.
- [ ] Task: Implement contradiction & consensus detection.
    - [ ] `DetectContradictions(ctx, grpcClient, findings []AnnotatedFinding) (string, error)`.
    - [ ] Local model identifies findings that reach conflicting conclusions.
    - [ ] Flags contested claims with supporting/opposing evidence.
    - [ ] Returns empty string if no contradictions found.
    - [ ] Red/Green TDD: TestDetectContradictions_FindsConflicts, TestDetectContradictions_NoneFound.
- [ ] Task: Implement gap analysis.
    - [ ] `AnalyzeGaps(ctx, grpcClient, findings []AnnotatedFinding, intent string) (string, error)`.
    - [ ] Local model identifies what the research *didn't* find relative to the intent.
    - [ ] Flags missing evidence, underrepresented populations, absent data types.
    - [ ] Red/Green TDD: TestGapAnalysis_IdentifiesGaps.
- [ ] Task: Implement suggested follow-up queries.
    - [ ] `SuggestFollowUp(ctx, grpcClient, findings []AnnotatedFinding, gaps, intent string) ([]string, error)`.
    - [ ] Local model generates 3-5 specific research questions based on gaps.
    - [ ] Red/Green TDD: TestSuggestFollowUp_ProducesQueries.
- [ ] Task: Implement recommended reading order.
    - [ ] `RecommendReadingOrder(ctx, grpcClient, findings []AnnotatedFinding, intent string) ([]string, error)`.
    - [ ] Local model suggests an ordered reading path with brief justification.
    - [ ] Red/Green TDD: TestReadingOrder_OrderedList.
- [ ] Task: Implement report compiler.
    - [ ] `CompileReport(plan *ResearchPlan, findings []AnnotatedFinding, sections ReportSections) string`.
    - [ ] Sort findings by relevance score (descending), then by date (newest first).
    - [ ] Star rating display (⭐ characters).
    - [ ] Include all sections: executive summary, source plan, findings, synthesis, contradictions, gap analysis, reading order, follow-up queries.
    - [ ] Distinguish primary findings from chased references in the listing.
    - [ ] Red/Green TDD: TestCompileReport_SortsByRelevance, TestCompileReport_IncludesAllSections.

## Phase 5b: Progress Persistence

### Objective
Save intermediate results to disk so crashes don't lose work, and retries resume from the last checkpoint.

### Tasks
- [ ] Task: Implement research work directory.
    - [ ] Create `.cercano/research/<topic-hash>/` for each run.
    - [ ] Hash from topic + intent + depth to identify unique runs.
    - [ ] Red/Green TDD: TestWorkDir_CreatedOnStart, TestWorkDir_DeterministicHash.
- [ ] Task: Implement phase checkpointing.
    - [ ] Save after each phase: `plan.json`, `search_results.json`, `findings.json`, `analysis.json`.
    - [ ] On start, check for existing work directory and skip completed phases.
    - [ ] Red/Green TDD: TestCheckpoint_SavesAndResumes, TestCheckpoint_SkipsCompletedPhases.
- [ ] Task: Implement cleanup.
    - [ ] Delete work directory after final report compilation (unless `output_path` is set).
    - [ ] If `output_path` is set, keep work directory alongside report for reference.
    - [ ] Red/Green TDD: TestCleanup_RemovesWorkDir, TestCleanup_KeepsWhenOutputPath.

## Phase 6: MCP Tool Handler & Integration

### Objective
Wire everything together as a `cercano_deep_research` MCP tool.

### Tasks
- [ ] Task: Add `DeepResearchRequest` struct and register `cercano_deep_research` tool.
    - [ ] Parameters: topic (required), intent (required), depth (optional), date_range (optional), sources (optional), output_path (optional), project_dir (optional).
    - [ ] Register in `registerTools()`.
    - [ ] Red/Green TDD: TestHandleDeepResearch_MissingTopic, TestHandleDeepResearch_MissingIntent.
- [ ] Task: Implement `handleDeepResearch` handler.
    - [ ] Orchestrate: plan sources → search → fetch → analyze → synthesize → compile.
    - [ ] If `output_path` set, write report to file and return summary.
    - [ ] If not, return full report (with warning if large).
    - [ ] Emit telemetry with content_tokens_avoided (sum of all fetched content).
    - [ ] Red/Green TDD: TestHandleDeepResearch_WritesToFile, TestHandleDeepResearch_ReturnsInline.
- [ ] Task: Add `cercano_deep_research` to `builtinSkills()` in `internal/server/skills.go`.
- [ ] Task: Create `.agents/skills/cercano-deep-research/SKILL.md`.
- [ ] Task: Conductor - User Manual Verification 'MCP Tool Handler & Integration' (Protocol in workflow.md)

## Phase 7: Documentation & Polish

### Objective
Update project docs and polish the output format.

### Tasks
- [ ] Task: Update README with `cercano_deep_research` tool description.
- [ ] Task: Conductor - User Manual Verification 'Documentation & Polish' (Protocol in workflow.md)
