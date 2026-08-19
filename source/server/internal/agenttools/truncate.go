package agenttools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Truncation policy, shared by agenttools.Result and capabilities.Result so the
// two parallel Result types cannot drift apart. See TruncateRows for why the
// byte ceiling exists in addition to the row ceiling.
const (
	// MaxRows bounds how many rows a result may carry.
	MaxRows = 200
	// MaxResultBytes bounds the serialized size of a result body. Text results
	// have always honored this; rows did not, which let a 107-row grep hit
	// ~346 KB and blow a 32k-token sub-agent window in a single call.
	MaxResultBytes = 32 * 1024
	// MaxRowValueBytes bounds any single value inside a row, so one minified
	// or generated line cannot consume the whole row budget on its own. The
	// incident's largest row was a 20,501-char line from a testdata fixture.
	MaxRowValueBytes = 2 * 1024
)

// TruncateUTF8 cuts s to at most maxBytes, stepping back off any UTF-8
// continuation byte so the result stays valid UTF-8.
func TruncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !isUTF8Boundary(s[cut]) {
		cut--
	}
	return s[:cut]
}

// RowsTruncation reports how TruncateRows capped a row set. Kept separate from
// the Result types so both can consume it.
type RowsTruncation struct {
	Rows       []map[string]any
	Truncated  bool
	TotalRows  int // rows before capping
	KeptRows   int // rows after capping
	TrimmedVal int // count of individual values shortened
	Note       string
}

// TruncateRows applies the row-count, row-value, and total-byte ceilings.
//
// Three ceilings rather than one because they fail differently: many small rows
// hit MaxRows, one pathological row hits MaxRowValueBytes, and a moderate number
// of large rows hits MaxResultBytes without tripping either of the others. The
// last case is what reached production — 107 rows, well under the 200-row cap,
// totalling ~346 KB.
//
// Rows are kept in order and never reordered; trimming a value marks the
// elision inline so the row stays readable and valid JSON.
func TruncateRows(rows []map[string]any) RowsTruncation {
	out := RowsTruncation{TotalRows: len(rows)}
	if len(rows) == 0 {
		out.Rows = rows
		return out
	}

	capped := rows
	if len(capped) > MaxRows {
		capped = capped[:MaxRows]
		out.Truncated = true
	}

	kept := make([]map[string]any, 0, len(capped))
	budget := MaxResultBytes
	for _, row := range capped {
		trimmed, n := trimRowValues(row)
		out.TrimmedVal += n

		size := approxRowBytes(trimmed)
		// Always keep the first row even if it alone exceeds the budget:
		// returning zero rows would make the model re-call the tool and burn
		// iterations, which is the failure this effort exists to prevent.
		if len(kept) > 0 && size > budget {
			out.Truncated = true
			break
		}
		kept = append(kept, trimmed)
		budget -= size
	}

	out.Rows = kept
	out.KeptRows = len(kept)
	if out.KeptRows < out.TotalRows {
		out.Truncated = true
	}
	out.Note = rowsNote(out)
	return out
}

// trimRowValues shortens any oversized string value in a row, returning a copy.
// Non-string values are left alone: they are bounded in practice (numbers,
// bools, small nested shapes) and rewriting them risks corrupting structure.
func trimRowValues(row map[string]any) (map[string]any, int) {
	trimmed := 0
	for k, v := range row {
		s, ok := v.(string)
		if !ok || len(s) <= MaxRowValueBytes {
			continue
		}
		if trimmed == 0 {
			// copy-on-write: only allocate once we know we must modify
			cp := make(map[string]any, len(row))
			for ck, cv := range row {
				cp[ck] = cv
			}
			row = cp
		}
		row[k] = TruncateUTF8(s, MaxRowValueBytes) + "… (value truncated)"
		trimmed++
	}
	return row, trimmed
}

// approxRowBytes measures a row's serialized cost. Falls back to a rough
// estimate if the row is not marshalable, so an exotic value cannot make the
// budget silently unbounded.
func approxRowBytes(row map[string]any) int {
	if b, err := json.Marshal(row); err == nil {
		return len(b)
	}
	n := 0
	for k, v := range row {
		n += len(k) + len(fmt.Sprint(v)) + 4
	}
	return n
}

// rowsNote builds the model-facing explanation. It names what was dropped and
// the remedy: the incident's query had neither path nor glob, so pointing at
// those is the most actionable part of the message.
func rowsNote(t RowsTruncation) string {
	var parts []string
	if t.KeptRows < t.TotalRows {
		parts = append(parts, fmt.Sprintf("showed %d of %d rows", t.KeptRows, t.TotalRows))
	}
	if t.TrimmedVal > 0 {
		parts = append(parts, fmt.Sprintf("%d oversized value(s) shortened", t.TrimmedVal))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + "; narrow with path/glob or a tighter pattern for the rest"
}
