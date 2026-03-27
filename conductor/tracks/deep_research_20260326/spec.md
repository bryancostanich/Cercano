# Track Specification: Deep Research Skill

## 1. Job Title
A multi-source research tool that takes a topic and intent, identifies authoritative sources, systematically searches each one, and compiles a ranked, annotated encyclopedia of findings.

## 2. Problem

The existing `cercano_research` tool does quick-and-dirty research: one DuckDuckGo search, a few page fetches, and a synthesized answer. It's great for quick lookups but falls short for deep investigation:

- It only searches one source (DuckDuckGo)
- It doesn't know *where* to look based on the domain (PubMed for biomedical, arXiv for ML/physics, IEEE for engineering, etc.)
- It doesn't rank or annotate findings
- It doesn't explain why each finding matters to the user's specific intent
- It doesn't produce a structured, reusable reference document

## 3. User Experience

### Input
```
cercano_deep_research(
  topic: "CRISPR gene therapy for sickle cell disease",
  intent: "I'm writing a grant proposal for a novel delivery mechanism. I need to understand the current state of the art, recent clinical trials, and gaps in the field.",
  depth: "thorough"  // or "survey" for a lighter pass
)
```

### Output
A structured markdown document:

```markdown
# Deep Research: CRISPR Gene Therapy for Sickle Cell Disease

## Research Intent
Writing a grant proposal for a novel delivery mechanism...

## Source Plan
The following sources were identified as relevant to this topic:
1. PubMed (clinical trials, peer-reviewed biomedical research)
2. ClinicalTrials.gov (active and completed trials)
3. arXiv/bioRxiv (preprints, cutting-edge methods)
4. Google Scholar (broad academic coverage)
5. FDA.gov (regulatory status, approvals)

Searched 5 sources, found 23 relevant publications.

---

## Findings

### 1. [Title of Paper/Article] ⭐⭐⭐⭐⭐
**Source:** PubMed | **Published:** 2025-11-15 | **Authors:** Smith et al.
**URL:** https://...

**Summary:** Brief summary of the finding.

**Why this matters to your research:**
This directly addresses the delivery mechanism challenge you're proposing to solve.
The study identified lipid nanoparticle limitations that your approach could overcome.

**Potential impact:** High — directly supports the gap analysis section of your proposal.

---

### 2. [Title of Paper/Article] ⭐⭐⭐⭐
...

## Executive Summary
[3-4 sentence TL;DR — the key takeaway for someone who needs the 30-second version
before deciding whether to read the full report]

## Synthesis
[2-3 paragraph narrative synthesizing the findings into a coherent picture,
highlighting the key themes and how they connect to the stated intent]

## Contradictions & Open Debates
- Papers A and B reach opposite conclusions about X. A argues... while B found...
- The efficacy of approach Y is contested: three studies support it, one challenges it.

## Gap Analysis
What the research *didn't* find — areas where evidence is missing or insufficient:
- No studies were found addressing delivery mechanisms in pediatric populations
- Long-term safety data (>5 years) is absent from all reviewed trials
- No open-source implementations of the proposed approach exist

## Recommended Reading Order
1. Start with [Paper X] for foundational context
2. Then [Paper Y] for the state of the art
3. [Paper Z] identifies the gap your proposal addresses

## Suggested Follow-Up Research
Based on the gaps identified and your stated intent, consider investigating:
1. "Lipid nanoparticle delivery in pediatric gene therapy" — addresses the pediatric gap
2. "Long-term outcomes of CRISPR-based sickle cell interventions" — safety data gap
3. "Open-source CRISPR delivery simulation models" — tooling gap for your proposal
```

### What Happens Locally vs Cloud

The **entire** research pipeline runs locally:
- Source planning (local model decides where to look)
- Web searching (DuckDuckGo + direct source APIs)
- Page fetching and content extraction
- Per-finding analysis and annotation
- Ranking and synthesis

The host agent receives only the final compiled document. This is potentially the highest-savings tool — dozens of page fetches and analyses that never touch the cloud.

## 4. Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `topic` | string | yes | The research topic |
| `intent` | string | yes | What the user needs this research for — drives relevance scoring and annotation |
| `depth` | string | no | `"survey"` (top 5-10 results, quick) or `"thorough"` (20+ results, deep). Default: `"thorough"` |
| `date_range` | string | no | Filter results by date. E.g. `"2024-2026"`, `"last 2 years"`, `"after 2023-06"`. Passed to APIs and used to filter web results. |
| `sources` | string[] | no | Override the auto-detected source list. If omitted, sources are chosen by the local model based on topic domain. |
| `output_path` | string | no | If provided, write the report to this file path instead of returning it inline. Recommended for thorough research. |
| `project_dir` | string | no | Project root for context |

## 5. Architecture

### Phase 1: Source Planning
The local model analyzes the topic + intent and produces a ranked list of sources to search. Each source has:
- **Name** (e.g. "PubMed", "arXiv", "Google Scholar")
- **Why** (why this source is relevant to the topic)
- **Search queries** (2-3 tailored queries per source)

Source types and their search mechanisms:

**Academic & Scientific:**
| Source | Search Method | Best For |
|--------|--------------|----------|
| PubMed | NCBI E-utilities API (free, no auth) | Biomedical, clinical |
| arXiv | arXiv API (free, no auth) | ML, physics, math, CS |
| bioRxiv/medRxiv | API (free, no auth) | Preprints, cutting-edge |
| Google Scholar | DuckDuckGo `site:scholar.google.com` | Broad academic |
| ClinicalTrials.gov | API (free, no auth) | Clinical trials |
| IEEE Xplore | DuckDuckGo `site:ieeexplore.ieee.org` | Engineering, electronics |
| SSRN | DuckDuckGo `site:ssrn.com` | Social sciences, economics, law |
| Semantic Scholar | API (free, no auth) | Cross-discipline, citation graph |

**Industry, Technology & Engineering:**
| Source | Search Method | Best For |
|--------|--------------|----------|
| GitHub | DuckDuckGo `site:github.com` | Open source implementations, codebases |
| Hacker News | DuckDuckGo `site:news.ycombinator.com` | Tech community discussion, emerging tools |
| Stack Overflow | DuckDuckGo `site:stackoverflow.com` | Technical Q&A, known issues |
| Patent databases | DuckDuckGo `site:patents.google.com` | IP landscape, prior art |

**News, Journalism & Popular Science:**
| Source | Search Method | Best For |
|--------|--------------|----------|
| Wired | DuckDuckGo `site:wired.com` | Technology journalism, trend analysis |
| Ars Technica | DuckDuckGo `site:arstechnica.com` | Deep technical reporting |
| Popular Science | DuckDuckGo `site:popsci.com` | Accessible science reporting |
| MIT Technology Review | DuckDuckGo `site:technologyreview.com` | Emerging tech analysis |
| Nature News | DuckDuckGo `site:nature.com/news` | Science news from Nature |
| The Atlantic | DuckDuckGo `site:theatlantic.com` | Long-form analysis, culture + science |
| New York Times | DuckDuckGo `site:nytimes.com` | Broad news coverage |

**Reference & Encyclopedic:**
| Source | Search Method | Best For |
|--------|--------------|----------|
| Wikipedia | DuckDuckGo `site:wikipedia.org` | Background context, terminology |
| Encyclopedia Britannica | DuckDuckGo `site:britannica.com` | Authoritative overviews |
| Stanford Encyclopedia of Philosophy | DuckDuckGo `site:plato.stanford.edu` | Philosophy, ethics, theory |

**Regulatory & Government:**
| Source | Search Method | Best For |
|--------|--------------|----------|
| FDA.gov | DuckDuckGo `site:fda.gov` | Drug/device regulatory status |
| WHO | DuckDuckGo `site:who.int` | Global health policy |
| NIH | DuckDuckGo `site:nih.gov` | US health research, funding |
| General web | DuckDuckGo | Anything not covered above |

For sources without direct APIs, we use DuckDuckGo with site-scoped queries. For PubMed, arXiv, bioRxiv, ClinicalTrials.gov, and Semantic Scholar, we use their free public APIs for better structured results.

The local model chooses which sources from this registry are relevant to the topic — a CRISPR query might hit PubMed, ClinicalTrials.gov, Nature News, and FDA.gov, while an ML query might hit arXiv, GitHub, Hacker News, and MIT Technology Review.

### Phase 2: Systematic Search
For each source in the plan:
1. Execute the tailored search queries
2. Collect result URLs and metadata (title, snippet, date)
3. Deduplicate across sources

### Phase 3: Content Extraction
For the top N results (based on depth setting):
1. Fetch the page content
2. Extract readable text (reuse existing `web.FetchURL`)
3. If it's a PDF abstract page, extract the abstract

### Phase 4: Analysis & Annotation
For each extracted result, the local model:
1. Summarizes the content (2-3 sentences)
2. Explains **why this matters** to the user's stated intent
3. Suggests **how it could be used** (potential applications)
4. Assigns a **relevance score** (1-5 stars) based on:
   - Direct relevance to the topic
   - Alignment with the stated intent
   - Recency
   - Source credibility
5. Assigns a **potential impact** rating (low/medium/high)
6. **Extracts cited references** — identifies works referenced in the content that appear relevant to the intent

### Phase 4b: Reference Chasing
When analyzing a finding, the local model may identify cited works that are directly relevant to the user's intent. These become secondary findings:

1. During Phase 4 analysis, the model extracts references: title, authors (if available), and why it's relevant
2. New references are added to a **chase queue** (deduplicated against existing findings)
3. For each queued reference, attempt to locate it:
   - Search by title on the original source (e.g., PubMed for a cited paper)
   - Fall back to DuckDuckGo title search
4. Fetch, analyze, and annotate the same way as primary findings
5. Mark these as "discovered via [Parent Finding Title]" in the report

**Depth limit:** 1 hop only — we chase references from primary findings but not from chased references. This prevents exponential blowup while still catching the most important related work.

**Budget:** Chase at most 5 references per primary finding, and at most 50 total chased references per run (configurable via depth setting: survey=10 total, thorough=50 total).

### Phase 5: Synthesis & Compilation
1. Sort findings by relevance score (descending)
2. Generate executive summary (local model — 3-4 sentences)
3. Generate narrative synthesis (local model — 2-3 paragraphs)
4. Detect contradictions — identify findings that reach conflicting conclusions
5. Perform gap analysis — identify what the research didn't find relative to the intent
6. Create a recommended reading order
7. Generate suggested follow-up research queries based on gaps
8. Compile into the final markdown document
9. If `output_path` is set, write to file; otherwise return inline

### Progress Persistence
A thorough run with reference chasing can take 10+ minutes. To avoid losing work on crashes or timeouts:

1. **Work directory**: Create `.cercano/research/<topic-hash>/` to store intermediate state
2. **Checkpoint after each phase**: Save the research plan, search results, fetched content, and analyzed findings as JSON files
3. **Resume support**: If the work directory exists with partial results, skip completed phases and resume from the last checkpoint
4. **Cleanup**: Delete the work directory after the final report is compiled (or keep it if `output_path` is set, for reference)

This means a crashed run can be retried by calling the same tool with the same parameters — it picks up where it left off.

## 6. API Integration Details

### PubMed (NCBI E-utilities)
- **Search**: `https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi?db=pubmed&term=QUERY&retmax=10&retmode=json`
- **Fetch**: `https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esummary.fcgi?db=pubmed&id=ID1,ID2&retmode=json`
- Free, no API key required (rate limit: 3 req/sec without key, 10/sec with)
- Returns structured metadata: title, authors, journal, date, abstract

### arXiv
- **Search**: `https://export.arxiv.org/api/query?search_query=QUERY&max_results=10`
- Returns Atom XML with title, authors, abstract, PDF links
- Free, no auth

### bioRxiv/medRxiv
- **Search**: `https://api.biorxiv.org/details/biorxiv/YYYY-MM-DD/YYYY-MM-DD/0/10`
- Or DuckDuckGo `site:biorxiv.org QUERY`
- Returns JSON with DOI, title, abstract

### ClinicalTrials.gov
- **Search**: `https://clinicaltrials.gov/api/v2/studies?query.term=QUERY&pageSize=10`
- Returns JSON with study details, status, phase

## 7. Token Savings Potential

This is potentially the highest-savings tool in Cercano:
- A thorough research run might fetch 20-30 pages (~100K tokens of content)
- Analyze each one locally (~500K total local inference tokens)
- Return a compiled report (~2K tokens to the host)
- **Estimated savings per run: 100K+ cloud tokens avoided**

## 8. Constraints & Risks

- **Latency**: A thorough run could take 2-5 minutes (many fetches + many model calls). Need progress reporting or async support.
- **Rate limits**: PubMed allows 3 req/sec without an API key. Need throttling.
- **Content quality**: Local models may misjudge relevance. The star ratings are estimates, not ground truth.
- **Fetching failures**: Some academic sites block scraping. Gracefully skip and note it.
- **Output size**: A thorough report could be large. The `output_path` parameter is important for keeping the MCP response small.

## 9. Non-Goals
- Full-text PDF downloading and analysis (just abstracts and metadata for now)
- Citation graph analysis (who cites whom)
- Automated bibliography formatting (BibTeX, etc.)
- Real-time monitoring of new publications
- Paid API integrations (Scopus, Web of Science)
