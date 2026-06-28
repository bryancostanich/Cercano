# Steering & Protocol Substrate — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Steer Cercano's own LLM toward plain English and toward following workflow protocols, and make one protocol library available across the standalone system prompt and the plugin skill catalog from a single source.

**Architecture:** A new `internal/protocols` package is the single source for protocol documents (ported from the `hardwAIr_hckr` "Dave" core skills + the generic `khalkulo/workflow` decision protocol). It produces three outputs from that one source: (1) an always-on **steering block** assembled into the standalone system prompt, (2) a `get_protocol` **capability** (built on Spec 0a) that returns a protocol body on demand on both surfaces, and (3) generated **SKILL.md files** for host discovery.

**Tech Stack:** Go 1.21+, `internal/capabilities` (Spec 0a), the existing system-prompt builder in `internal/server/server.go`, the existing Agent Skills layout (`.agents/skills/`, `.claude/skills/`).

## Global Constraints

- **Depends on Spec 0a:** the `get_protocol` capability (Phase 3) requires `internal/capabilities` and `builtins.Register` from Spec 0a. Phases 1–2 and 4 do not depend on 0a and can land first.
- Single source: protocol text lives once in `internal/protocols`. The steering triggers, the `get_protocol` body, and the generated `SKILL.md` files are all derived from it — no second copy.
- The steering block is **standalone-only** (Cercano cannot edit a host's system prompt). The plugin channel is the generated `SKILL.md` files + `get_protocol`.
- Plain-English steering text must follow the project's plain-English rule: no LLM/code shorthand, spell out acronyms, read like a colleague.
- The watchdog (Spec 0b Part C) is **out of scope** for this plan — see "Deferred" at the end.
- Commit messages must not contain the word "Claude".
- `go test ./...` green in `source/server` after every task.

---

## Phase 1 — Protocol library (`internal/protocols`)

### Task 1: Protocol type + registry

**Files:**
- Create: `source/server/internal/protocols/protocols.go`
- Test: `source/server/internal/protocols/protocols_test.go`

**Interfaces:**
- Produces: `Domain` (`DomainCore`/`DomainHardware`), `Protocol{Name, Description string; Domain Domain; Trigger, Body string}`, `Builtins() []Protocol`, `Get(name string) (Protocol, bool)`, `ForDomain(Domain) []Protocol`.

- [ ] **Step 1: Write the failing test**

```go
package protocols

import "testing"

func TestGetAndForDomain(t *testing.T) {
	all := Builtins()
	if len(all) < 4 {
		t.Fatalf("expected >=4 builtin protocols, got %d", len(all))
	}
	p, ok := Get("design-decisions")
	if !ok {
		t.Fatal("design-decisions not found")
	}
	if p.Trigger == "" || p.Body == "" {
		t.Fatal("design-decisions missing trigger/body")
	}
	for _, c := range ForDomain(DomainCore) {
		if c.Domain != DomainCore {
			t.Fatalf("ForDomain returned non-core: %s", c.Name)
		}
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("unknown protocol should not be found")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/protocols/ -v`
Expected: FAIL — package does not compile (`Builtins` undefined).

- [ ] **Step 3: Write the implementation**

```go
// Package protocols is the single source for Cercano's workflow protocols.
// One protocol document feeds three outputs: the always-on steering block
// (Trigger lines), the get_protocol capability (Body), and generated SKILL.md
// files for host discovery. Ported from the hardwAIr_hckr "Dave" core skills
// and the generic khalkulo/workflow decision protocol.
package protocols

import "sort"

// Domain separates generic protocols from domain-specific ones so the default
// steering set can exclude hardware protocols.
type Domain string

const (
	DomainCore     Domain = "core"
	DomainHardware Domain = "hardware"
)

// Protocol is one workflow discipline.
type Protocol struct {
	Name        string // kebab-case id, e.g. "design-decisions"
	Description string // one-line, for skill discovery
	Domain      Domain
	Trigger     string // one-line always-on rule (feeds the steering block)
	Body        string // the full protocol markdown (pulled on demand)
}

// Builtins returns the built-in protocol catalog, sorted by name.
func Builtins() []Protocol {
	out := append([]Protocol(nil), builtinProtocols...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a protocol by name.
func Get(name string) (Protocol, bool) {
	for _, p := range builtinProtocols {
		if p.Name == name {
			return p, true
		}
	}
	return Protocol{}, false
}

// ForDomain returns protocols in the given domain, sorted by name.
func ForDomain(d Domain) []Protocol {
	var out []Protocol
	for _, p := range Builtins() {
		if p.Domain == d {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 4: Add a stub catalog so it compiles**

Create `source/server/internal/protocols/catalog.go` with an empty slice for now (filled in Task 2):

```go
package protocols

var builtinProtocols = []Protocol{}
```

- [ ] **Step 5: Run test to verify it fails on the count assertion**

Run: `cd source/server && go test ./internal/protocols/ -v`
Expected: FAIL — `expected >=4 builtin protocols, got 0` (compiles now; content comes in Task 2).

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/protocols/protocols.go source/server/internal/protocols/catalog.go source/server/internal/protocols/protocols_test.go
git commit -m "feat(protocols): Protocol type + registry (empty catalog)"
```

### Task 2: Port the four core protocols into the catalog

**Files:**
- Modify: `source/server/internal/protocols/catalog.go`
- Test: `source/server/internal/protocols/catalog_test.go`

Port four protocols. **Bodies** are copied from the source files below (content, not code); **Triggers** are the one-liners specified here verbatim; **frontmatter is not stored in `Body`** (the SKILL.md generator in Phase 4 adds frontmatter from `Name`/`Description`).

| Name | Domain | Body source | Description |
|---|---|---|---|
| `design-decisions` | core | **merge** (see below) | Stop and weigh real options before coding a structural decision. |
| `systematic-debugging` | core | `hardwAIr_hckr/plugins/skills/core/systematic-debugging/SKILL.md` (body after frontmatter) | Reduce, observe, reason, predict, probe, then fix — no guessing. |
| `verification-strategy` | core | `hardwAIr_hckr/plugins/skills/core/verification-strategy/SKILL.md` (body) | Match the test tier to the size of the change. |
| `compute-before-simulate` | core | `hardwAIr_hckr/plugins/skills/core/compute-before-simulate/SKILL.md` (body) | Compute the expected result before running any simulation or sweep. |

**`design-decisions` merge:** use `khalkulo/workflow/design_decisions.md` as the base (it is the richer 7-step version: it adds the **symmetric quantification rule** inside step 3 and **step 5 "Argue against your own recommendation"**, neither of which the Dave version has). Genericize its cost-unit examples — replace chip-specific units (`gates`, `registers`, `mm²`, `SRAM`, `address space`) with general software terms (`lines of code`, `files touched`, `new dependencies`, `test surface`), keeping the hardware examples only as parenthetical asides if useful. Keep all 7 steps, the "What Counts as a Hack" list, and the "Why This Exists" section.

**Triggers (verbatim):**

- `design-decisions`: `Facing a real decision with more than one viable approach → stop, enumerate the real options with their trade-offs in plain English, and get human approval before writing code.`
- `systematic-debugging`: `Before applying any fix to a bug or test failure → reduce to the smallest failing case, observe the actual data, and confirm the root cause with a probe; never fix on reasoning alone.`
- `verification-strategy`: `Match the test tier to the change — don't run the full end-to-end suite for an internal change, and don't skip integration tests when an interface changed.`
- `compute-before-simulate`: `Before running a simulation, benchmark, or parameter sweep → compute the expected result analytically first; the run verifies the math, it doesn't replace it.`

- [ ] **Step 1: Write the failing test**

```go
package protocols

import (
	"strings"
	"testing"
)

func TestCoreCatalogComplete(t *testing.T) {
	want := []string{"compute-before-simulate", "design-decisions", "systematic-debugging", "verification-strategy"}
	for _, name := range want {
		p, ok := Get(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if p.Domain != DomainCore {
			t.Fatalf("%s should be core", name)
		}
		if len(strings.TrimSpace(p.Body)) < 200 {
			t.Fatalf("%s body looks too short to be the real protocol", name)
		}
		if !strings.HasSuffix(p.Trigger, ".") {
			t.Fatalf("%s trigger should be a full sentence", name)
		}
	}
}

func TestDesignDecisionsHasMergedSteps(t *testing.T) {
	p, _ := Get("design-decisions")
	// The two khalkulo-only additions must be present after the merge.
	if !strings.Contains(p.Body, "Symmetric quantification") && !strings.Contains(p.Body, "symmetric quantification") {
		t.Fatal("design-decisions missing the symmetric quantification rule")
	}
	if !strings.Contains(strings.ToLower(p.Body), "argue against your own recommendation") {
		t.Fatal("design-decisions missing the argue-against-yourself step")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/protocols/ -run TestCore -v`
Expected: FAIL — catalog is empty.

- [ ] **Step 3: Fill the catalog**

Replace `catalog.go`'s `builtinProtocols` with the four protocols. Bodies are Go raw-string literals (backtick); where a body itself contains backticks (code fences), break the raw string and concatenate with `"`...`"` exactly as the existing `skills.go` does. Skeleton:

```go
package protocols

var builtinProtocols = []Protocol{
	{
		Name:        "design-decisions",
		Domain:      DomainCore,
		Description: "Stop and weigh real options before coding a structural decision.",
		Trigger:     "Facing a real decision with more than one viable approach → stop, enumerate the real options with their trade-offs in plain English, and get human approval before writing code.",
		Body: `# Design Decision Protocol
... (merged 7-step body from khalkulo/workflow/design_decisions.md, genericized) ...`,
	},
	{
		Name:        "systematic-debugging",
		Domain:      DomainCore,
		Description: "Reduce, observe, reason, predict, probe, then fix — no guessing.",
		Trigger:     "Before applying any fix to a bug or test failure → reduce to the smallest failing case, observe the actual data, and confirm the root cause with a probe; never fix on reasoning alone.",
		Body: `# Systematic Debugging Protocol
... (body from hardwAIr_hckr core/systematic-debugging/SKILL.md, after frontmatter) ...`,
	},
	{
		Name:        "verification-strategy",
		Domain:      DomainCore,
		Description: "Match the test tier to the size of the change.",
		Trigger:     "Match the test tier to the change — don't run the full end-to-end suite for an internal change, and don't skip integration tests when an interface changed.",
		Body: `# Verification Strategy Protocol
... (body from hardwAIr_hckr core/verification-strategy/SKILL.md, after frontmatter) ...`,
	},
	{
		Name:        "compute-before-simulate",
		Domain:      DomainCore,
		Description: "Compute the expected result before running any simulation or sweep.",
		Trigger:     "Before running a simulation, benchmark, or parameter sweep → compute the expected result analytically first; the run verifies the math, it doesn't replace it.",
		Body: `# Compute Before Simulate
... (body from hardwAIr_hckr core/compute-before-simulate/SKILL.md, after frontmatter) ...`,
	},
}
```

Paste the actual bodies from the source files (do not leave the `...` placeholders). For `design-decisions`, paste the genericized merge described above.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/protocols/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/protocols/catalog.go source/server/internal/protocols/catalog_test.go
git commit -m "feat(protocols): port 4 core protocols (decision/debug/verify/compute)"
```

---

## Phase 2 — Steering block + injection

### Task 3: Steering block assembler

**Files:**
- Create: `source/server/internal/protocols/steering.go`
- Test: `source/server/internal/protocols/steering_test.go`

**Interfaces:**
- Produces: `SteeringBlock(ps []Protocol) string`.

- [ ] **Step 1: Write the failing test**

```go
package protocols

import (
	"strings"
	"testing"
)

func TestSteeringBlockContainsRulesAndTriggers(t *testing.T) {
	ps := []Protocol{
		{Name: "a", Trigger: "Trigger A here."},
		{Name: "b", Trigger: "Trigger B here."},
	}
	out := SteeringBlock(ps)
	if !strings.Contains(out, "plain English") {
		t.Fatal("missing plain-English rule")
	}
	if strings.Count(out, "Trigger A here.")+strings.Count(out, "Trigger B here.") != 2 {
		t.Fatal("both triggers must appear exactly once")
	}
	// Adding a protocol adds exactly one line.
	before := strings.Count(out, "\n")
	out2 := SteeringBlock(append(ps, Protocol{Name: "c", Trigger: "Trigger C here."}))
	if strings.Count(out2, "\n") != before+1 {
		t.Fatal("adding a protocol should add exactly one trigger line")
	}
}

func TestSteeringBlockEmptyProtocols(t *testing.T) {
	out := SteeringBlock(nil)
	if !strings.Contains(out, "plain English") {
		t.Fatal("rules must be present even with no protocols")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/protocols/ -run TestSteering -v`
Expected: FAIL — `SteeringBlock` undefined.

- [ ] **Step 3: Write the implementation**

```go
package protocols

import "strings"

// plainEnglishRules are the always-on steering rules. Kept terse and concrete.
const plainEnglishRules = `Communication and working rules:
- Write in plain English. Present decisions, options, and trade-offs as clear prose a colleague would use — not terse model shorthand or jargon. Spell out acronyms the first time.
- When you face a choice or report a trade-off, lay out the real alternatives and your reasoning, then recommend one.`

// SteeringBlock assembles the always-on steering text: the fixed plain-English
// rules followed by one trigger line per protocol. The block is generated from
// the protocols themselves so the rules and the library never drift.
func SteeringBlock(ps []Protocol) string {
	var b strings.Builder
	b.WriteString(plainEnglishRules)
	if len(ps) > 0 {
		b.WriteString("\n\nWorkflow protocols — when one of these applies, pull the full protocol with the get_protocol tool and follow it:")
		for _, p := range ps {
			b.WriteString("\n- ")
			b.WriteString(p.Trigger)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/protocols/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/protocols/steering.go source/server/internal/protocols/steering_test.go
git commit -m "feat(protocols): steering-block assembler (plain-English rules + triggers)"
```

### Task 4: Inject the steering block into the system prompt

**Files:**
- Modify: `source/server/internal/server/server.go` (`buildToolLoopSystem` ~1037, `buildSystemPrompt` ~1076)
- Test: `source/server/internal/server/steering_prompt_test.go`

**Interfaces:**
- Consumes: `protocols.SteeringBlock`, `protocols.ForDomain(protocols.DomainCore)`.
- Changes: `buildToolLoopSystem` gains a `steering string` parameter inserted after the persona line.

- [ ] **Step 1: Write the failing test**

```go
package server

import (
	"strings"
	"testing"
)

func TestSystemPromptIncludesSteering(t *testing.T) {
	s := &Server{}
	// workDir empty so the optional directory/project-context blocks stay out.
	out := s.buildSystemPrompt("")
	if !strings.Contains(out, "plain English") {
		t.Fatal("system prompt missing the plain-English steering rule")
	}
	if !strings.Contains(out, "get_protocol") {
		t.Fatal("system prompt missing the protocol-trigger steering")
	}
	// Persona stays first.
	if !strings.HasPrefix(out, "You are Cercano") {
		t.Fatal("persona line must remain first")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/server/ -run TestSystemPromptIncludesSteering -v`
Expected: FAIL — steering not present.

- [ ] **Step 3: Edit `buildToolLoopSystem` to take and insert the steering block**

Change the signature and insert the block right after the persona line (between current lines 1039 and 1040):

```go
func buildToolLoopSystem(env loopEnv, steering, dirSnapshot, projectContext string) string {
	var b strings.Builder
	b.WriteString("You are Cercano, an agentic coding assistant operating in a terminal.\n\n")
	if strings.TrimSpace(steering) != "" {
		b.WriteString(steering)
		b.WriteString("\n\n")
	}
	b.WriteString("<env>\n")
	// ... unchanged ...
```

- [ ] **Step 4: Edit `buildSystemPrompt` to build and pass the steering block**

```go
func (s *Server) buildSystemPrompt(workDir string) string {
	env := loopEnv{
		WorkDir:  workDir,
		Platform: runtime.GOOS,
		Date:     time.Now().Format("2006-01-02"),
	}
	if workDir != "" {
		env.GitRepo, env.GitBranch = gitInfo(workDir)
	}
	steering := protocols.SteeringBlock(protocols.ForDomain(protocols.DomainCore))
	return buildToolLoopSystem(env, steering, directorySnapshot(workDir, 80), s.loadProjectContext(workDir))
}
```

Add the import `"cercano/source/server/internal/protocols"` to `server.go`.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd source/server && go test ./internal/server/ -run TestSystemPromptIncludesSteering -v`
Expected: PASS.

- [ ] **Step 6: Build and run the full suite**

Run: `cd source/server && make build && go test ./... -count=1`
Expected: builds; PASS (other tests that call `buildToolLoopSystem` directly, if any, must be updated for the new parameter — `grep -rn buildToolLoopSystem` and fix call sites/tests).

- [ ] **Step 7: Commit**

```bash
git add source/server/internal/server/server.go source/server/internal/server/steering_prompt_test.go
git commit -m "feat(server): inject steering block (plain English + protocol triggers) into system prompt"
```

---

## Phase 3 — On-demand retrieval: `get_protocol` capability

> **Requires Spec 0a** (`internal/capabilities` + `builtins.Register`). If 0a is not yet
> merged, do Phases 1–2 and 4 first and return here after 0a lands.

### Task 5: `get_protocol` capability

**Files:**
- Create: `source/server/internal/capabilities/builtins/get_protocol.go`
- Modify: `source/server/internal/capabilities/builtins/builtins.go` (register it)
- Test: `source/server/internal/capabilities/builtins/get_protocol_test.go`

**Interfaces:**
- Consumes: `protocols.Get`, `protocols.Builtins`, `capabilities.Capability`.
- Produces: `GetProtocol() capabilities.Capability` (canonical name `get_protocol`, tier R, surfaces agent+mcp).

- [ ] **Step 1: Write the failing test**

```go
package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestGetProtocolReturnsBody(t *testing.T) {
	cap := GetProtocol()
	if cap.Name() != "get_protocol" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}
	if !cap.Surfaces().Has(capabilities.SurfaceAgent) || !cap.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Fatal("get_protocol should be on both surfaces")
	}
	args, _ := json.Marshal(map[string]any{"name": "design-decisions"})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Design Decision Protocol") {
		t.Fatalf("body not returned: %q", res.Text[:min(80, len(res.Text))])
	}
}

func TestGetProtocolUnknownLists(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"name": "nope"})
	_, err := GetProtocol().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for unknown protocol")
	}
	if !strings.Contains(err.Error(), "design-decisions") {
		t.Fatal("error should list available protocol names")
	}
}

func min(a, b int) int { if a < b { return a }; return b }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/capabilities/builtins/ -run TestGetProtocol -v`
Expected: FAIL — `GetProtocol` undefined.

- [ ] **Step 3: Write the implementation**

```go
package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/protocols"
)

type getProtocolCap struct{}

// GetProtocol constructs the get_protocol capability: returns a workflow
// protocol's full body by name.
func GetProtocol() capabilities.Capability { return getProtocolCap{} }

func (getProtocolCap) Name() string                  { return "get_protocol" }
func (getProtocolCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (getProtocolCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (getProtocolCap) Description() string {
	return "Return the full text of a Cercano workflow protocol by name (e.g. design-decisions, systematic-debugging, verification-strategy, compute-before-simulate). Pull a protocol when its trigger applies, then follow it."
}
func (getProtocolCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","required":["name"],"properties":{"name":{"type":"string","description":"Protocol name, e.g. design-decisions."}}}`)
}

type getProtocolArgs struct {
	Name string `json:"name"`
}

func (getProtocolCap) Execute(_ context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a getProtocolArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("get_protocol: parse args: %w", err)
	}
	if a.Name == "" {
		return nil, errors.New("get_protocol: name is required")
	}
	p, ok := protocols.Get(a.Name)
	if !ok {
		var names []string
		for _, b := range protocols.Builtins() {
			names = append(names, b.Name)
		}
		return nil, fmt.Errorf("get_protocol: unknown protocol %q; available: %s", a.Name, strings.Join(names, ", "))
	}
	return capabilities.NewTextResult(p.Body), nil
}
```

- [ ] **Step 4: Register it**

In `builtins/builtins.go`, add to `Register`:

```go
	reg.MustRegister(GetProtocol())
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd source/server && go test ./internal/capabilities/builtins/ -run TestGetProtocol -v`
Expected: PASS.

- [ ] **Step 6: Build and full suite**

Run: `cd source/server && make build && go test ./... -count=1`
Expected: builds; PASS. `get_protocol` now appears as a standalone tool and (via the Spec 0a MCP adapter) as `cercano_get_protocol`.

- [ ] **Step 7: Commit**

```bash
git add source/server/internal/capabilities/builtins/get_protocol.go source/server/internal/capabilities/builtins/get_protocol_test.go source/server/internal/capabilities/builtins/builtins.go
git commit -m "feat(capabilities): get_protocol capability (on-demand protocol body, both surfaces)"
```

---

## Phase 4 — Plugin discovery: SKILL.md generation

### Task 6: Generate protocol SKILL.md files + `cercano protocols sync`

**Files:**
- Create: `source/server/internal/protocols/skillgen.go`
- Create: `source/server/internal/protocols/skillgen_test.go`
- Modify: `source/server/cmd/cercano/main.go` (add a `protocols sync` subcommand)

**Interfaces:**
- Produces: `WriteSkillFiles(rootDir string) ([]string, error)` — writes each protocol to `<rootDir>/.agents/skills/<name>/SKILL.md` and `<rootDir>/.claude/skills/<name>/SKILL.md`, returns the paths written.

- [ ] **Step 1: Write the failing test**

```go
package protocols

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSkillFiles(t *testing.T) {
	root := t.TempDir()
	written, err := WriteSkillFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("no files written")
	}
	p := filepath.Join(root, ".agents", "skills", "design-decisions", "SKILL.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected %s: %v", p, err)
	}
	s := string(data)
	if !strings.HasPrefix(s, "---\nname: design-decisions\n") {
		t.Fatal("missing/incorrect frontmatter")
	}
	if !strings.Contains(s, "Design Decision Protocol") {
		t.Fatal("body not written")
	}
	// Mirror tree also written.
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "design-decisions", "SKILL.md")); err != nil {
		t.Fatalf(".claude mirror missing: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/protocols/ -run TestWriteSkillFiles -v`
Expected: FAIL — `WriteSkillFiles` undefined.

- [ ] **Step 3: Write the implementation**

```go
package protocols

import (
	"fmt"
	"os"
	"path/filepath"
)

// skillTrees are the on-disk skill directories hosts discover.
var skillTrees = []string{".agents/skills", ".claude/skills"}

// WriteSkillFiles renders every builtin protocol as a SKILL.md under each skill
// tree below rootDir, so host agents (Claude Code, etc.) discover the protocols
// natively. Frontmatter is generated from Name/Description; the body is the
// protocol Body. Returns the paths written.
func WriteSkillFiles(rootDir string) ([]string, error) {
	var written []string
	for _, p := range Builtins() {
		content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", p.Name, p.Description, p.Body)
		for _, tree := range skillTrees {
			dir := filepath.Join(rootDir, tree, p.Name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return written, fmt.Errorf("protocols: mkdir %s: %w", dir, err)
			}
			path := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return written, fmt.Errorf("protocols: write %s: %w", path, err)
			}
			written = append(written, path)
		}
	}
	return written, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/protocols/ -v`
Expected: PASS.

- [ ] **Step 5: Add the `protocols sync` subcommand**

In `cmd/cercano/main.go`, where subcommands are dispatched (next to `agent`/`run`/`setup`/`--mcp`), add a `protocols` command:

```go
case "protocols":
	// cercano protocols sync [dir]  — write protocol SKILL.md files for host discovery
	if len(os.Args) >= 3 && os.Args[2] == "sync" {
		root := "."
		if len(os.Args) >= 4 {
			root = os.Args[3]
		}
		written, err := protocols.WriteSkillFiles(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "protocols sync:", err)
			os.Exit(1)
		}
		fmt.Printf("Wrote %d protocol skill files under %s\n", len(written), root)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: cercano protocols sync [dir]")
	os.Exit(2)
```

Add the import for `protocols` and match the existing main.go subcommand-dispatch style (the exact `switch`/`if` shape — `grep -n '"setup"' cmd/cercano/main.go` to find it).

- [ ] **Step 6: Build and verify the command**

Run: `cd source/server && make build && bin/cercano protocols sync /tmp/cercano-protocols && ls /tmp/cercano-protocols/.agents/skills`
Expected: prints the count; lists the four protocol dirs.

- [ ] **Step 7: Commit**

```bash
git add source/server/internal/protocols/skillgen.go source/server/internal/protocols/skillgen_test.go source/server/cmd/cercano/main.go
git commit -m "feat(protocols): generate protocol SKILL.md files + 'cercano protocols sync'"
```

---

## Deferred — the watchdog (Spec 0b Part C)

The watchdog (a small, separately-configured model that monitors the agent and enforces
protocols via challenge-and-justify) is **designed in the spec but not implemented here.**
It is deferred to its own plan because it depends on the small-model routing defined by
the **subagent execution engine (Tier 2)**, which does not exist yet. Building it now would
mean inventing that routing ahead of its own design. When Tier 2 lands, write a separate
plan for the watchdog against this protocol library (the `Trigger`/`Body` split and
`get_protocol` are exactly what it consumes). No placeholder code is added here.

---

## Self-Review

- **Spec coverage:** protocol library single source (Tasks 1–2), plain-English steering + protocol triggers (Task 3), standalone injection (Task 4), `get_protocol` on-demand pull on both surfaces (Task 5), generated SKILL.md for host discovery (Task 6). The watchdog is explicitly deferred with a rationale (not a placeholder).
- **Single source honored:** triggers, `get_protocol` body, and SKILL.md files all derive from `internal/protocols`; no second copy of protocol text.
- **Dependency flagged:** Phase 3 requires Spec 0a; Phases 1–2 and 4 are independent and can land first.
- **Placeholder scan:** the only `...` markers are in Task 2's catalog skeleton, with an explicit instruction to paste the real bodies from named source files — not a deliverable placeholder.
- **Type consistency:** `Protocol`, `Domain`/`DomainCore`, `Builtins`, `Get`, `ForDomain`, `SteeringBlock`, `WriteSkillFiles`, `GetProtocol` used consistently across tasks; `buildToolLoopSystem` signature change (added `steering` param) is applied at its sole call site in Task 4 with a grep to catch other callers.
</content>
