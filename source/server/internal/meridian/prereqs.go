package meridian

import (
	"os/exec"
	"strconv"
	"strings"
)

// minNodeMajor is the lowest Node.js major version that Meridian supports
// (mirrors @rynfar/meridian's package.json "engines": "node >=22").
const minNodeMajor = 22

// Prereqs is a snapshot of the external tools Meridian needs to run and the
// Claude Code CLI it needs to authenticate. Each field is independently
// resolvable; "ready" means Node is present and meets the version floor.
type Prereqs struct {
	NodePath     string // empty if not on PATH
	NodeVersion  string // raw "v22.10.0" string from `node --version`
	NodeOK       bool   // true if NodePath != "" AND major >= minNodeMajor
	ClaudePath   string // empty if not on PATH (npx fallback still works for auth)
	ClaudeOK     bool   // true if ClaudePath != ""
	MissingNotes []string
}

// DetectPrereqs probes PATH for `node` and `claude` and inspects node's
// version. It never spawns anything heavy and never opens a browser.
func DetectPrereqs() Prereqs {
	return detectPrereqsWith(realLookPath, realExec)
}

// lookPathFn matches exec.LookPath so tests can stub it.
type lookPathFn func(name string) (string, error)

func realLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func detectPrereqsWith(lookPath lookPathFn, run execFn) Prereqs {
	p := Prereqs{}
	if path, err := lookPath("node"); err == nil {
		p.NodePath = path
		if out, err := run(path, "--version"); err == nil {
			p.NodeVersion = strings.TrimSpace(string(out))
			if major, ok := parseNodeMajor(p.NodeVersion); ok && major >= minNodeMajor {
				p.NodeOK = true
			} else {
				p.MissingNotes = append(p.MissingNotes,
					"Node "+p.NodeVersion+" is too old (need "+strconv.Itoa(minNodeMajor)+"+)")
			}
		} else {
			p.MissingNotes = append(p.MissingNotes, "node --version failed: "+err.Error())
		}
	} else {
		p.MissingNotes = append(p.MissingNotes,
			"Node.js "+strconv.Itoa(minNodeMajor)+"+ not on PATH (install: brew install node)")
	}
	if path, err := lookPath("claude"); err == nil {
		p.ClaudePath = path
		p.ClaudeOK = true
	}
	// claude is recoverable via `npx -y @anthropic-ai/claude-code`, so its
	// absence isn't a hard miss — just no MissingNotes entry.
	return p
}

// parseNodeMajor extracts the major version from a "v22.10.0" style string.
// Returns (0, false) if the input doesn't look like a Node version.
func parseNodeMajor(v string) (int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '.'); i > 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}
