package render

import "strings"

// MdKind tags a streamed markdown block.
type MdKind int

const (
	MdProse MdKind = iota // prose, headings, lists, code fences — rendered via Glamour
	MdTable               // a pipe-table — rendered via the responsive Table renderer
)

// MdBlock is one block of a streamed assistant reply.
type MdBlock struct {
	Kind  MdKind
	Raw   string // markdown source (MdProse)
	Table *Table // set when Kind == MdTable
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
	inFence := false

	flushProse := func() {
		if len(cur) > 0 {
			blocks = append(blocks, MdBlock{Kind: MdProse, Raw: strings.Join(cur, "\n")})
			cur = nil
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]

		if !inFence {
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
		}

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			cur = append(cur, line)
			i++
			continue
		}
		if trimmed == "" && !inFence {
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

// tableTerminated reports whether a table starting at lines[i] spanning
// `consumed` lines is finished streaming: either a line follows it, or the
// buffer ended with a newline (so the last row is complete).
func tableTerminated(lines []string, i, consumed int, hadTrailingNL bool) bool {
	if i+consumed < len(lines) {
		return true
	}
	return hadTrailingNL
}
