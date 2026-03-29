# Track Specification: Deep Research Enhancement

## 1. Job Title
Improve the analytical quality of `cercano_deep_research` output to produce genuinely useful research — specific, insightful, and cross-referenced — rather than structurally correct but shallow summaries.

## 2. Problem

The current deep research pipeline produces output that is:
- **Structurally sound** — correct formatting, proper sections, star ratings, all the right fields
- **Analytically shallow** — summaries read like book reports, not genuine analysis. "This directly addresses the competitive landscape" is filler, not insight.
- **Isolated** — each finding is analyzed independently with no awareness of other findings. Can't draw connections, contrasts, or identify patterns across sources.
- **Uncritical** — relevance scores are inflated (everything gets 4-5 stars). The model doesn't discriminate.
- **Accepted at face value** — when the model produces a vague summary, the pipeline accepts it. No quality check, no re-prompting.

Claude produces better research because it naturally:
- Iterates and refines ("wait, that's too vague")
- Cross-references findings as it reads them
- Goes deeper on fewer sources rather than skimming many
- Applies genuine analytical reasoning, not template-filling

## 3. Root Causes

### 3a. Single-pass, overloaded prompts
The analysis prompt asks the model to do 7 things at once (summarize, extract key findings, explain relevance, suggest usage, score, rate impact, extract references). Small models degrade when asked to do too many things simultaneously. The output for each section gets thinner.

### 3b. No cross-finding context
Finding #15 is analyzed with zero knowledge of findings #1-14. The model can't say "unlike ExecuTorch's approach..." or "this corroborates finding #3's claim that..." — connections that make research actually useful.

### 3c. No quality gate
When the model produces "This is relevant to the competitive landscape" (a content-free sentence), the pipeline records it and moves on. There's no check for whether the output is actually useful.

### 3d. Breadth over depth
The pipeline searches 6 sources and processes 50 results with shallow analysis. Better research would go deeper on fewer sources — read more content per finding, spend more model calls analyzing each one.

### 3e. Model mismatch
`qwen3-coder` is a coding model. It's excellent at structured output and following format instructions, but shallow on analytical reasoning. The prompts are written as if Claude is running them.

## 4. Proposed Fixes

### Fix 1: Multi-pass analysis pipeline
Replace the single analysis call with three focused passes:

**Pass 1 — Fact extraction:** "Read this content and extract every concrete fact, number, method, result, and conclusion. Return a bullet list of facts only."

**Pass 2 — Relevance analysis:** "Given these facts about [title] and the user's intent [intent], explain specifically how this finding relates. What's the connection? What's the implication?"

**Pass 3 — Critique & refine:** "Review this analysis. Is the summary specific enough to be useful? Does it contain concrete facts someone could cite? If not, identify what's vague and rewrite it with more specificity."

Three simple prompts > one complex prompt for small models.

### Fix 2: Cross-finding context window
When analyzing finding N, include a brief context block:

```
Previously analyzed findings (for cross-reference):
1. ExecuTorch — 50KB footprint, 12+ backends, AOT compilation
2. LiteRT — Google's TFLite successor, edge deployment
3. torchchat — local LLM runner, Python-based
...

How does the current finding relate to, contrast with, or build on these?
```

This enables the model to draw connections. Limited to 1-line summaries to keep context small.

### Fix 3: Quality gate with re-prompting
After analysis, run a quality check:

```
Review this summary: "[summary]"
Does it contain:
- Specific facts, numbers, or metrics? (not just "this is relevant")
- Concrete methods or approaches described?
- Actionable information someone could use?

Score: PASS or FAIL
If FAIL, explain what's vague.
```

On FAIL, re-prompt with the critique: "The previous summary was too vague. Specifically: [critique]. Rewrite with more concrete detail."

Max 1 retry to avoid infinite loops.

### Fix 4: Depth over breadth
Adjust the defaults:
- **Survey:** 3-4 sources, max 10 results, deeper analysis per finding
- **Thorough:** 5-6 sources, max 20 results, deeper analysis per finding
- Increase content truncation from 8K to 12K chars
- Spend 3-4 model calls per finding (extract, analyze, critique) instead of 1

The total model calls go up, but each individual call is simpler and produces better output.

### Fix 5: Model-appropriate prompts with examples
Include a concrete example in each prompt showing what good vs bad output looks like:

```
BAD summary: "This paper presents a novel approach to local inference that is relevant to the competitive landscape."
GOOD summary: "ExecuTorch achieves a 50KB base footprint by using ahead-of-time compilation and supports 12 hardware backends. On Pixel 8, it runs Llama 3.1 8B at 15 tokens/sec with 4-bit quantization."
```

Small models learn much better from examples than from abstract instructions.

## 5. Success Criteria

A successful enhancement produces findings where:
- Summaries contain **specific numbers, methods, and conclusions** — not generic descriptions
- Cross-references appear: "Unlike X, this tool..." or "Corroborates finding #N's claim..."
- Relevance scores show real discrimination — some findings get 2/5, not everything 4-5/5
- Gap analysis identifies **specific, non-obvious** gaps, not generic "lack of benchmarks"
- A reader could act on the research without going back to the original sources

## 6. Constraints

- Must work with `qwen3-coder` — can't require a different model
- Latency will increase (more model calls per finding) — acceptable tradeoff
- Token usage will increase ~3x per finding — acceptable given the quality improvement
- Must be backward compatible — same MCP interface, same output format
