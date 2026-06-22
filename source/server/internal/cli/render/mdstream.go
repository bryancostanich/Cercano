package render

import "strings"

// MdKind tags a streamed markdown block.
type MdKind int

const (
	MdProse MdKind = iota // prose, headings, lists — rendered via Glamour
	MdTable               // a pipe-table — rendered via the responsive Table renderer
	MdCode                // a fenced code block — Glamour-rendered, wrapped in rules
)

// MdBlock is one block of a streamed assistant reply.
type MdBlock struct {
	Kind  MdKind
	Raw   string // markdown source (MdProse / MdCode, fences included)
	Table *Table // set when Kind == MdTable
	Lang  string // fence info string (e.g. "go"); set when Kind == MdCode
}

// SplitBlocks splits markdown into completed blocks plus a trailing in-progress
// tail. Completed blocks are stable across frames (cacheable); the tail changes
// as tokens arrive and is rendered live.
//
// Rules: a blank line (outside a code fence) ends the current prose block; a
// code fence suspends blank-line splitting until it closes; a terminated
// pipe-table is its own MdTable block; a single trailing newline is the cursor
// position, not a separator.
func SplitBlocks(text string) (blocks []MdBlock, tail string) {
	lines := strings.Split(text, "\n")
	hadTrailingNL := strings.HasSuffix(text, "\n")
	// A final newline produces a trailing "" element — that's the cursor
	// position, not a blank-line separator. Drop it.
	if hadTrailingNL && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var cur []string

	flushProse := func() {
		if len(cur) > 0 {
			blocks = append(blocks, MdBlock{Kind: MdProse, Raw: strings.Join(cur, "\n")})
			cur = nil
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Fenced code block → its own block once the closing fence arrives.
		if strings.HasPrefix(trimmed, "```") {
			if lang, consumed := matchFence(lines, i); consumed > 0 {
				flushProse()
				blocks = append(blocks, MdBlock{
					Kind: MdCode,
					Raw:  strings.Join(lines[i:i+consumed], "\n"),
					Lang: lang,
				})
				i += consumed
				continue
			}
			// Unterminated fence — the rest is the still-streaming tail.
			cur = append(cur, lines[i:]...)
			break
		}

		// Terminated pipe-table → its own block.
		if mt, consumed := matchTable(lines, i); consumed > 0 && tableTerminated(lines, i, consumed, hadTrailingNL) {
			flushProse()
			tbl := mt.toTable()
			blocks = append(blocks, MdBlock{
				Kind:  MdTable,
				Raw:   strings.Join(lines[i:i+consumed], "\n"),
				Table: &tbl,
			})
			i += consumed
			continue
		}

		if trimmed == "" {
			flushProse()
			i++
			continue
		}
		cur = append(cur, line)
		i++
	}

	tail = strings.Join(cur, "\n")
	return blocks, tail
}

// matchFence consumes a fenced code block starting at lines[i] (an opening
// ``` line). Returns the fence info string (language) and the number of lines
// consumed including the closing fence, or 0 if no closing fence is present yet
// (the block is still streaming).
func matchFence(lines []string, i int) (lang string, consumed int) {
	open := strings.TrimSpace(lines[i])
	if !strings.HasPrefix(open, "```") {
		return "", 0
	}
	lang = strings.TrimSpace(strings.TrimPrefix(open, "```"))
	for j := i + 1; j < len(lines); j++ {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
			return lang, j - i + 1
		}
	}
	return lang, 0
}

// tableTerminated reports whether a table starting at lines[i] spanning
// `consumed` lines is finished streaming: either a line follows it, or the
// buffer ended with a newline (so the last row is complete).
func tableTerminated(lines []string, i, consumed int, hadTrailingNL bool) bool {
	if i+consumed < len(lines) {
		return true
	}
	return hadTrailingNL
}
