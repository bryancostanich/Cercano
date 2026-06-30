package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// promptImage is one image attached to the prompt. The prompt text carries an
// "[image <id>]" marker; the registry is append-only per prompt and Attachments()
// returns only those whose marker still appears in the text.
type promptImage struct {
	id        int
	data      []byte
	mediaType string
	source    string // file path, or "" for clipboard
}

type imageSpan struct {
	start int // rune index of '['
	end   int // rune index just past ']'
	id    int
}

var promptImageMarkerRe = regexp.MustCompile(`\[image (\d+)\]`)

func imageMarker(id int) string { return fmt.Sprintf("[image %d]", id) }

// AddImage registers an image and inserts its marker at the cursor.
func (p *promptInput) AddImage(data []byte, mediaType, source string) {
	p.nextImageID++
	id := p.nextImageID
	p.attachments = append(p.attachments, promptImage{id: id, data: data, mediaType: mediaType, source: source})
	p.InsertString(imageMarker(id))
}

// RegisterImage registers an image with an existing marker id already present
// in the prompt text, without inserting a new marker. Used when restoring a
// queued turn so existing "[image N]" markers resolve without duplication.
// If id is greater than nextImageID, nextImageID is advanced past it.
func (p *promptInput) RegisterImage(id int, data []byte, mediaType, source string) {
	p.attachments = append(p.attachments, promptImage{id: id, data: data, mediaType: mediaType, source: source})
	if id > p.nextImageID {
		p.nextImageID = id
	}
}

// liveIDs is the set of attachment ids currently registered.
func (p promptInput) liveIDs() map[int]bool {
	out := make(map[int]bool, len(p.attachments))
	for _, a := range p.attachments {
		out[a.id] = true
	}
	return out
}

// imageSpans returns rune-offset spans for every "[image N]" marker in the text
// whose id is a live attachment, in order of appearance.
func (p promptInput) imageSpans() []imageSpan {
	if len(p.attachments) == 0 {
		return nil
	}
	live := p.liveIDs()
	s := string(p.value)
	var spans []imageSpan
	for _, m := range promptImageMarkerRe.FindAllStringSubmatchIndex(s, -1) {
		id, _ := strconv.Atoi(s[m[2]:m[3]])
		if !live[id] {
			continue
		}
		start := utf8.RuneCountInString(s[:m[0]])
		end := start + utf8.RuneCountInString(s[m[0]:m[1]])
		spans = append(spans, imageSpan{start: start, end: end, id: id})
	}
	return spans
}

// Attachments returns the registered images whose marker still appears in the
// text, in marker order (deduped).
func (p promptInput) Attachments() []promptImage {
	byID := make(map[int]promptImage, len(p.attachments))
	for _, a := range p.attachments {
		byID[a.id] = a
	}
	var out []promptImage
	seen := make(map[int]bool)
	for _, sp := range p.imageSpans() {
		if seen[sp.id] {
			continue
		}
		if a, ok := byID[sp.id]; ok {
			out = append(out, a)
			seen[sp.id] = true
		}
	}
	return out
}

// spanForBackspace returns the chip span to delete when backspace is pressed at
// the current cursor: a span ending at the cursor, or one the cursor sits inside.
func (p promptInput) spanForBackspace() (imageSpan, bool) {
	for _, sp := range p.imageSpans() {
		if p.cursor == sp.end || (p.cursor > sp.start && p.cursor < sp.end) {
			return sp, true
		}
	}
	return imageSpan{}, false
}

// spanForDeleteForward returns the chip span to delete on forward-delete: a span
// starting at the cursor, or one the cursor sits inside.
func (p promptInput) spanForDeleteForward() (imageSpan, bool) {
	for _, sp := range p.imageSpans() {
		if p.cursor == sp.start || (p.cursor > sp.start && p.cursor < sp.end) {
			return sp, true
		}
	}
	return imageSpan{}, false
}

// deleteSpan removes a chip's marker text. The attachment is left in the
// registry (harmless, append-only); Attachments() ignores it once the marker is
// gone, and undo that restores the marker re-includes it.
func (p *promptInput) deleteSpan(sp imageSpan) {
	p.value = append(append([]rune{}, p.value[:sp.start]...), p.value[sp.end:]...)
	p.cursor = sp.start
	p.selectionAnchor = noPromptSelection
}

// stepLeftOverChip snaps an offset that landed inside a chip back to the chip
// start, so a left-arrow treats the chip as a single position.
func (p promptInput) stepLeftOverChip(offset int) int {
	for _, sp := range p.imageSpans() {
		if offset > sp.start && offset < sp.end {
			return sp.start
		}
	}
	return offset
}

// stepRightOverChip snaps an offset inside a chip forward to the chip end.
func (p promptInput) stepRightOverChip(offset int) int {
	for _, sp := range p.imageSpans() {
		if offset > sp.start && offset < sp.end {
			return sp.end
		}
	}
	return offset
}
