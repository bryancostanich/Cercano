package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/gitflow"
)

// requestPlanApprovalCap is the model-invoked handoff out of planning mode.
// The model calls it only after it has authored the effort's spec.md and plan.md
// and is ready for the human to approve execution.
//
// It is X-tier on purpose: the standard tool confirm gate fires BEFORE Execute
// even in Permissive and Bypass modes, giving the user the y/n/d/c prompt. Approving runs Execute, which first commits the
// effort's spec.md and plan.md to git on the current branch (and ONLY those
// files — unrelated staged/unstaged/untracked changes are never swept in),
// then leaves the read-only planning profile (back to the unrestricted
// default). The commit happens before the profile switch so that a failed
// commit keeps the session in planning mode instead of silently stranding
// untracked planning docs on disk — untracked docs on main are exactly what
// blocks a later worktree/landing fast-forward. Declining means Execute never
// runs; the model receives the denial and can revise the plan or continue
// discussing it.
//
// This capability does not implement the execution driver loop. Step 4 is the
// handoff only: approval checkpoints the docs and drops the fence so normal
// implementation can begin. The dedicated execute profile / divergence
// classifier is step 5.
type requestPlanApprovalCap struct{}

// RequestPlanApproval returns the capability.
func RequestPlanApproval() capabilities.Capability { return requestPlanApprovalCap{} }

func (requestPlanApprovalCap) Name() string { return "request_plan_approval" }

// TierX so the confirm gate is the approval prompt even in Permissive and Bypass modes.
func (requestPlanApprovalCap) Tier() capabilities.Tier { return capabilities.TierX }

func (requestPlanApprovalCap) Surfaces() capabilities.Surface {
	// Agent surface only: approving a session-local plan handoff is meaningless
	// over MCP.
	return capabilities.SurfaceAgent
}

func (requestPlanApprovalCap) Description() string {
	return "Request human approval to leave planning mode and begin executing the written plan. Call this only after you have written the effort's spec.md and plan.md and summarized what will be executed. The effort is required — the declared effort's spec.md and plan.md are the files approval checkpoints; to abandon planning without a plan, call plan_exit instead. The user is shown the standard y/n/d/c prompt. In a Git repository, approval commits only the declared spec.md and plan.md to git on the current branch before leaving planning mode — other local changes are left untouched — and a failed commit keeps the session in planning mode with an error explaining what to fix; outside a Git repository approval simply leaves planning mode without committing. After approval, ask the execution-style follow-up by calling request_autonomous_execution with a concise autonomous run brief, or continue step-by-step if the user declines autonomous execution."
}

func (requestPlanApprovalCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["effort", "summary"],
		"properties": {
			"effort": {"type": "string", "description": "Effort directory or slug, e.g. \"efforts/migrate-config-loader\". Used to locate and commit the planning docs."},
			"summary": {"type": "string", "description": "Concise human-readable summary of the plan to execute."},
			"plan_path": {"type": "string", "description": "Optional path to the plan file, usually efforts/<slug>/plan.md. Must be a repo-relative regular file inside the effort directory; it is committed to git on approval. Defaults to efforts/<effort>/plan.md."},
			"spec_path": {"type": "string", "description": "Optional path to the spec file, usually efforts/<slug>/spec.md. Must be a repo-relative regular file inside the effort directory; it is committed to git on approval. Defaults to efforts/<effort>/spec.md."}
		}
	}`)
}

type requestPlanApprovalArgs struct {
	Effort   string `json:"effort"`
	Summary  string `json:"summary"`
	PlanPath string `json:"plan_path"`
	SpecPath string `json:"spec_path"`
}

func (requestPlanApprovalCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	// Reaching Execute means the user approved at the confirm gate.
	var a requestPlanApprovalArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("request_plan_approval: parse args: %w", err)
		}
	}
	if call.Svc.EnterProfile == nil {
		return nil, fmt.Errorf("request_plan_approval: session profile switching is not available (no profile broker wired)")
	}

	// Checkpoint the planning docs BEFORE dropping the planning profile. If the
	// commit fails we return an error here, while the session is still fenced
	// to planning — the user has not "left" anywhere, and the model can fix the
	// git problem and retry. Committing first is what keeps the docs from being
	// stranded as untracked files on the current branch.
	effort := strings.TrimSpace(a.Effort)
	if effort == "" {
		// The effort names whose spec.md/plan.md get checkpointed; without it
		// there is nothing safe to commit. Abandoning planning is plan_exit's
		// job, not this approval gate's.
		return nil, fmt.Errorf("request_plan_approval: effort is required — approval checkpoints the declared effort's spec.md and plan.md to git; if you are abandoning the plan instead, call plan_exit. The session stays in planning mode")
	}
	gitNote, err := commitPlanningDocs(ctx, call, effort, &a)
	if err != nil {
		return nil, err
	}
	if err := call.Svc.EnterProfile(call.ConversationID, "default"); err != nil {
		return nil, fmt.Errorf("request_plan_approval: leaving planning mode: %w", err)
	}

	parts := []string{"Plan approved. Left planning mode; normal implementation tools are available. Before beginning implementation, call request_autonomous_execution with a concise autonomous run brief from the approved spec.md/plan.md. That single follow-up asks: \"Plan approved. Execute it autonomously with this run brief?\" If the user says yes, autonomous mode starts from that approval; if no, proceed step-by-step under the executing-plans protocol."}
	if gitNote != "" {
		parts = append(parts, gitNote)
	}
	if effort != "" {
		parts = append(parts, "Effort: "+effort)
	}
	if summary := strings.TrimSpace(a.Summary); summary != "" {
		parts = append(parts, "Summary: "+summary)
	}
	if spec := strings.TrimSpace(a.SpecPath); spec != "" {
		parts = append(parts, "Spec: "+spec)
	}
	if plan := strings.TrimSpace(a.PlanPath); plan != "" {
		parts = append(parts, "Plan: "+plan)
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: strings.Join(parts, "\n")}, nil
}

// commitPlanningDocs resolves the effort's spec/plan paths and commits them on
// the current branch. It returns a human-readable note for the result text and
// an error when the handoff must NOT proceed (unsafe paths, missing docs, or
// a failed commit — all of which keep the session in planning mode, since this
// runs before the profile switch).
func commitPlanningDocs(ctx context.Context, call *capabilities.Call, effort string, a *requestPlanApprovalArgs) (string, error) {
	effortDir, err := normalizeEffortDir(effort)
	if err != nil {
		return "", err
	}
	workDir := strings.TrimSpace(call.WorkDir)
	if workDir == "" {
		// Without a working directory the project (and therefore the effort's
		// docs and the repository) cannot be reliably resolved — there is no
		// safe checkpoint and no honest "already handled" claim either.
		return "", fmt.Errorf("request_plan_approval: no working directory is available for this session, so the effort's planning docs cannot be located or committed — cannot reliably resolve the project; the session stays in planning mode")
	}
	r, err := gitflow.Open(workDir)
	if err != nil {
		// Not a git work tree: same established repo-less handoff, reported
		// honestly instead of claiming a checkpoint.
		return "Planning docs were NOT committed to git: the working directory is not a git work tree. Commit spec.md/plan.md by hand before any worktree or landing flow.", nil
	}

	// Resolve spec and plan paths. Explicit paths must exist and stay inside
	// the declared effort directory; omitted paths default under it.
	specPath, err := resolvePlanningDocPath(workDir, effortDir, strings.TrimSpace(a.SpecPath), "spec.md")
	if err != nil {
		return "", err
	}
	planPath, err := resolvePlanningDocPath(workDir, effortDir, strings.TrimSpace(a.PlanPath), "plan.md")
	if err != nil {
		return "", err
	}
	paths := []string{specPath, planPath}
	if specPath == planPath {
		paths = paths[:1]
	}

	subject := "checkpoint: planning approval for " + effortDir
	body := "Approved " + strings.Join(paths, " and ") + " via request_plan_approval; committed before exiting planning mode so worktree/landing flows are not blocked by untracked planning docs."
	sha, committed, err := r.CommitPlanningDocs(ctx, paths, subject, body)
	if err != nil {
		return "", fmt.Errorf("request_plan_approval: committing planning docs (%s) failed: %w — the session stays in planning mode; resolve the git problem and call request_plan_approval again", strings.Join(paths, ", "), err)
	}
	if !committed {
		return "Planning docs were already committed to git and clean on the current branch; no new commit was needed.", nil
	}
	branch, err := r.CurrentBranch(ctx)
	if err != nil {
		branch = "the current branch"
	}
	short := sha
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("Planning docs committed to git on branch %s (commit %s): %s. Only these files were committed; other local changes were left untouched.", branch, short, strings.Join(paths, ", ")), nil
}

// normalizeEffortDir turns an effort argument ("efforts/foo" or the bare slug
// "foo") into a clean slash path rooted at efforts/. It rejects absolute
// paths, ~, and anything that escapes the efforts/ tree.
func normalizeEffortDir(effort string) (string, error) {
	e := strings.TrimSpace(effort)
	if e == "" {
		return "", fmt.Errorf("request_plan_approval: effort is required to checkpoint the planning docs")
	}
	if strings.HasPrefix(e, "/") || strings.HasPrefix(e, "~") {
		return "", fmt.Errorf("request_plan_approval: invalid effort %q: must be a relative effort directory or slug under efforts/, not an absolute path", effort)
	}
	e = strings.TrimPrefix(e, "./")
	if !strings.HasPrefix(e, "efforts/") {
		e = "efforts/" + e
	}
	e = filepath.ToSlash(filepath.Clean(e))
	if !strings.HasPrefix(e, "efforts/") {
		return "", fmt.Errorf("request_plan_approval: invalid effort %q: resolves outside the efforts/ tree", effort)
	}
	return e, nil
}

// resolvePlanningDocPath validates an explicit planning-doc path or falls back
// to <effortDir>/<defaultName>. Explicit paths must be repo-relative regular
// files inside the declared effort directory; defaults must exist on disk.
// Symlinks are rejected via Lstat — both the doc itself and every parent
// directory under the work dir — because a symlink can point outside the
// repository and would smuggle uncheckpointable content into the commit.
func resolvePlanningDocPath(workDir, effortDir, explicit, defaultName string) (string, error) {
	if explicit == "" {
		p := effortDir + "/" + defaultName
		if err := requireRegularFile(workDir, p); err != nil {
			return "", fmt.Errorf("request_plan_approval: planning doc %s not found under %s — write it (or pass an explicit path inside the effort directory) before requesting approval: %w", defaultName, effortDir, err)
		}
		return p, nil
	}
	if strings.HasPrefix(explicit, "/") || strings.HasPrefix(explicit, "~") {
		return "", fmt.Errorf("request_plan_approval: invalid planning doc path %q: must be a repo-relative path inside %s, not absolute", explicit, effortDir)
	}
	clean := filepath.ToSlash(filepath.Clean(explicit))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("request_plan_approval: unsafe planning doc path %q: resolves outside the repository", explicit)
	}
	rel, err := filepath.Rel(effortDir, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("request_plan_approval: planning doc path %q is outside the declared effort directory %s", explicit, effortDir)
	}
	if err := requireRegularFile(workDir, clean); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("request_plan_approval: planning doc %s not found: %w", explicit, err)
		}
		return "", fmt.Errorf("request_plan_approval: planning doc %s: %w", explicit, err)
	}
	return clean, nil
}

// requireRegularFile verifies that rel names an existing regular file under
// workDir with no symlink anywhere on the path from workDir down to the file
// (the file itself, or any parent directory component, being a symlink is an
// error). Lstat is used throughout so symlinks are seen for what they are
// instead of being silently resolved to their targets.
func requireRegularFile(workDir, rel string) error {
	// Walk every path component from workDir down to rel, rejecting symlinks.
	cur := workDir
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink — planning docs must be real files inside the effort directory, not symlinks", rel)
		}
	}
	info, err := os.Lstat(filepath.Join(workDir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", rel)
	}
	return nil
}
