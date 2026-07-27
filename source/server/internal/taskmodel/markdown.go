package taskmodel

import (
	"fmt"
	"strings"
)

// This file is the lossless codec between a Conductor-style plan.md and the
// in-memory Task tree (planning-mode design §3.3). The file is canon; the tree is
// its parse. The grammar:
//
//	# Effort Title            -> root task; prose until the first ## is its Notes
//	<context prose>
//
//	## Phase Title            -> a phase (child of root); prose until the first
//	<objective / files / tests>   checkbox or next heading is the phase's Notes
//
//	- [ ] Task title          -> a task; nesting by 2-space indent per level
//	  - [x] Sub-task title    -> a sub-task (child of the task above it)
//	    <note line>           -> continuation prose becomes that task's Notes
//
// Status glyphs: " " pending, "x" done, "~" in_progress, "-" blocked.
//
// Fidelity contract: this is a SEMANTIC round-trip, not a byte-exact one. Parsing
// then serializing reproduces the structured content — titles, status, hierarchy,
// and every node's Notes prose — but re-normalizes incidental formatting (indent
// is emitted as two spaces per level; runs of blank lines collapse). Serialize is
// therefore idempotent: Serialize(Parse(Serialize(t))) == Serialize(t). We do not
// store raw formatting trivia on Task, because the node is deliberately pure
// (design §2); byte-exactness would require polluting it, so we don't chase it.

const indentUnit = "  " // two spaces per nesting level

// statusGlyph maps a Status to its checkbox glyph.
var statusGlyph = map[Status]string{
	StatusPending:    " ",
	StatusDone:       "x",
	StatusInProgress: "~",
	StatusBlocked:    "-",
}

// glyphStatus is the inverse of statusGlyph.
var glyphStatus = map[string]Status{
	" ": StatusPending,
	"x": StatusDone,
	"X": StatusDone, // tolerate uppercase on input
	"~": StatusInProgress,
	"-": StatusBlocked,
}

// idFn generates a task ID. It is a package var so tests can make IDs
// deterministic; production wiring can swap in a ULID/UUID generator.
var idFn = defaultIDFn

// defaultIDFn derives a slug-ish stable-ish ID from a running counter and the
// title. Parsing needs *some* ID per node (the model requires non-empty, unique
// IDs); the file itself carries no IDs, so we synthesize them at parse time.
func defaultIDFn(seq int, title string) string {
	slug := slugify(title)
	if slug == "" {
		return fmt.Sprintf("t%d", seq)
	}
	return fmt.Sprintf("%s-%d", slug, seq)
}

func slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ParsePlan parses Conductor-format Markdown into a Task tree rooted at the
// effort. The document must have exactly one top-level `#` heading; that becomes
// the root. Phases (`##`) become the root's children; checkbox items become tasks
// and sub-tasks nested by indentation. Returns an error if the document has no
// `#` heading, more than one, or malformed structure (e.g. a sub-task indented
// under nothing).
func ParsePlan(md string) (Task, error) {
	p := &planParser{seq: 0}
	return p.parse(md)
}

type planParser struct {
	seq int
}

func (p *planParser) nextID(title string) string {
	p.seq++
	return idFn(p.seq, title)
}

func (p *planParser) parse(md string) (Task, error) {
	lines := strings.Split(md, "\n")

	var (
		root     *Task
		curPhase *Task
		// stack of the checkbox ancestors by indent level; stack[0] is a
		// depth-0 task under the current phase, stack[1] its sub-task, etc.
		stack    []*Task
		notesFor **Task // where trailing prose lines currently accumulate
	)

	// notesBuf accumulates raw prose lines for the current owner; flushed on the
	// next structural line.
	var notesBuf []string
	flushNotes := func() {
		if notesFor == nil || *notesFor == nil {
			notesBuf = nil
			return
		}
		joined := strings.TrimRight(strings.Join(notesBuf, "\n"), "\n")
		joined = strings.TrimSpace(joined)
		if joined != "" {
			(*notesFor).Notes = joined
		}
		notesBuf = nil
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "# "):
			if root != nil {
				return Task{}, fmt.Errorf("taskmodel: plan has more than one top-level '#' heading")
			}
			flushNotes()
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			r := Task{ID: p.nextID(title), Title: title, Status: StatusPending}
			root = &r
			notesFor = &root
			curPhase = nil
			stack = nil

		case strings.HasPrefix(trimmed, "## "):
			if root == nil {
				return Task{}, fmt.Errorf("taskmodel: phase heading before the effort '#' heading")
			}
			flushNotes()
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			phase := Task{ID: p.nextID(title), Title: title, Status: StatusPending}
			added := root.AddChild(phase)
			curPhase = added
			notesFor = &curPhase
			stack = nil

		case isCheckbox(line):
			flushNotes()
			if curPhase == nil {
				return Task{}, fmt.Errorf("taskmodel: checkbox %q appears before any '## phase' heading", trimmed)
			}
			level, status, title, err := parseCheckbox(line)
			if err != nil {
				return Task{}, err
			}
			if level > len(stack) {
				return Task{}, fmt.Errorf("taskmodel: checkbox %q is over-indented (jumps more than one level)", trimmed)
			}
			task := Task{ID: p.nextID(title), Title: title, Status: status}
			// truncate the stack to the parent level
			stack = stack[:level]
			var attached *Task
			if level == 0 {
				attached = curPhase.AddChild(task)
			} else {
				parent := stack[level-1]
				attached = parent.AddChild(task)
			}
			stack = append(stack, attached)
			notesFor = &stack[len(stack)-1]

		default:
			// Blank or prose line: accumulate as notes for the current owner.
			notesBuf = append(notesBuf, line)
		}
	}
	flushNotes()

	if root == nil {
		return Task{}, fmt.Errorf("taskmodel: plan has no top-level '#' heading")
	}
	return *root, nil
}

// isCheckbox reports whether a line (with leading indent) is a checkbox item.
func isCheckbox(line string) bool {
	t := strings.TrimLeft(line, " ")
	return strings.HasPrefix(t, "- [") && len(t) >= 5 && t[4] == ']'
}

// parseCheckbox extracts the nesting level (from indent), status, and title from
// a checkbox line. Two leading spaces = one level.
func parseCheckbox(line string) (level int, status Status, title string, err error) {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	level = indent / 2
	t := strings.TrimLeft(line, " ")
	// t looks like "- [x] title"
	glyph := string(t[3]) // char between the brackets
	st, ok := glyphStatus[glyph]
	if !ok {
		return 0, "", "", fmt.Errorf("taskmodel: unknown status glyph %q in %q", glyph, strings.TrimSpace(line))
	}
	title = strings.TrimSpace(t[5:]) // after "- [x]"
	return level, st, title, nil
}

// SerializePlan renders a Task tree back to Conductor-format Markdown. The root
// is the effort (`#`), its children are phases (`##`), and everything below a
// phase is checkbox items nested by depth. Each node's Notes prose is emitted
// beneath its heading/checkbox. Output is canonical: two-space indents, single
// blank lines between sections, trailing newline.
func SerializePlan(root Task) string {
	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(root.Title)
	b.WriteString("\n")
	if notes := strings.TrimSpace(root.Notes); notes != "" {
		b.WriteString("\n")
		b.WriteString(notes)
		b.WriteString("\n")
	}

	for pi := range root.Children {
		phase := &root.Children[pi]
		b.WriteString("\n## ")
		b.WriteString(phase.Title)
		b.WriteString("\n")
		if notes := strings.TrimSpace(phase.Notes); notes != "" {
			b.WriteString("\n")
			b.WriteString(notes)
			b.WriteString("\n")
		}
		if len(phase.Children) > 0 {
			b.WriteString("\n")
			for ti := range phase.Children {
				writeTaskCheckbox(&b, &phase.Children[ti], 0)
			}
		}
	}

	return b.String()
}

// writeTaskCheckbox writes one checkbox item at the given depth and recurses into
// its children. A task's Notes prose is written as indented continuation lines
// beneath the checkbox.
func writeTaskCheckbox(b *strings.Builder, t *Task, depth int) {
	indent := strings.Repeat(indentUnit, depth)
	glyph, ok := statusGlyph[t.Status]
	if !ok {
		glyph = " " // defensive: unknown status renders as pending
	}
	b.WriteString(indent)
	b.WriteString("- [")
	b.WriteString(glyph)
	b.WriteString("] ")
	b.WriteString(t.Title)
	b.WriteString("\n")

	if notes := strings.TrimSpace(t.Notes); notes != "" {
		noteIndent := strings.Repeat(indentUnit, depth+1)
		for _, ln := range strings.Split(notes, "\n") {
			b.WriteString(noteIndent)
			b.WriteString(ln)
			b.WriteString("\n")
		}
	}

	for ci := range t.Children {
		writeTaskCheckbox(b, &t.Children[ci], depth+1)
	}
}
