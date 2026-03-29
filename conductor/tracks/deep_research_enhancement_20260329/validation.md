# Deep Research Enhancement — Before/After Validation

## Test Parameters
- **Topic:** "local AI inference for developer tools"
- **Intent:** "Understanding the competitive landscape for Cercano"
- **Depth:** survey
- **Date:** 2026-03-29

## Before: qwen3-coder, single-pass analysis

| Metric | Value |
|--------|-------|
| Model | qwen3-coder (18.6 GB, code-optimized) |
| Findings | 47 primary + 3 references = 50 total |
| Sources | 6 |

### Score Distribution
| Score | Count | % |
|-------|-------|---|
| 5/5 | 2 | 4% |
| 4/5 | 29 | 58% |
| 3/5 | 7 | 14% |
| 2/5 | 9 | 18% |
| 1/5 | 3 | 6% |

### Summary Quality
- **Vague summaries common:** "Specific performance metrics or benchmarks. Concrete numbers or statistics." (literally echoed the prompt instructions as content)
- **Key findings parroted prompt format** instead of actual facts
- **Relevance inflated** — 62% scored 4-5/5 despite many findings being irrelevant (Anki flashcards, W3C federated learning issues)
- **No cross-references** between findings
- **Search quality poor** — many results not about local AI developer tools at all

### Synthesis Quality
- Structurally correct but shallow
- Names many tools but doesn't analyze WHY they matter
- Bold-formatted tool names give appearance of depth without substance

## After: qwen2.5:72b, multi-pass analysis with enhancements

| Metric | Value |
|--------|-------|
| Model | qwen2.5:72b (47.4 GB, general-purpose) |
| Findings | 21 primary + 0 references = 21 total |
| Sources | 4 |
| Analysis passes | 3 per finding (extract → relevance → quality gate) |

### Score Distribution
| Score | Count | % |
|-------|-------|---|
| 5/5 | 0 | 0% |
| 4/5 | 17 | 81% |
| 3/5 | 4 | 19% |
| 2/5 | 0 | 0% |
| 1/5 | 0 | 0% |

### Summary Quality
- **Specific facts present:** "NVIDIA DGX Spark achieving 70% reduction in inference latency and 35% improvement in power efficiency"
- **Named tools with context:** "Framework Desktop supports running Llama 3.3 70B Q6 at real-time conversational speeds"
- **Quantization specifics:** "standardize on quantized model files (Q4_0, Q5_0) to reduce memory footprints"
- **Why It Matters sections are genuine analysis** — explains connections between findings and user intent
- **Search results more relevant** — Twinny, llamacpp, ipex-llm, DGX Spark are all actually about local inference for dev tools

### Synthesis Quality
- **Genuinely analytical** — identifies the performance-vs-accessibility gap as a market opportunity
- **Draws connections:** DGX Spark (high-end) vs Framework Desktop (consumer) vs Twinny (free/OSS)
- **Actionable insight:** "Cercano can differentiate by bridging the gap between high performance and accessibility"
- **Privacy theme identified** as a differentiator across multiple findings

## Improvements Measured

| Dimension | Before | After | Change |
|-----------|--------|-------|--------|
| Findings count | 50 | 21 | Fewer but deeper |
| Irrelevant findings | ~15 (30%) | ~3 (14%) | 2x improvement in relevance |
| Vague/empty summaries | ~10 (20%) | ~2 (10%) | 2x fewer |
| Specific numbers in summaries | ~30% of findings | ~60% of findings | 2x more specific |
| Synthesis references named tools | Yes (bold formatting) | Yes (with context/numbers) | Qualitative improvement |
| Cross-finding connections | None | Limited (not strong yet) | New capability |
| Score discrimination | 1-5 range used | 3-4 range only | Still needs work |

## Remaining Issues

1. **Score clustering** — moved from everything-is-4-5 to everything-is-4. The model doesn't use the full 1-5 range enough. Need stronger calibration in the relevance prompt.
2. **Some thin findings** — GitHub topic pages and archived repos produce thin content. Could filter these out based on content length before analyzing.
3. **Cross-references still weak** — the cross-context is passed in but the model rarely draws explicit connections. May need a dedicated cross-reference pass.
4. **Reference chasing produced 0 results** in the after run — the quality gate may be too aggressive with citation extraction.

## Conclusion

The model switch (qwen3-coder → qwen2.5:72b) produced the largest quality improvement. The multi-pass pipeline, better search queries, and example-driven prompts contributed incrementally. The combination is significantly better than the baseline but still has room for improvement in score calibration and cross-referencing.
