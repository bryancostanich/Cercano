// Package contextedit turns a natural-language instruction plus a conversation's
// turn summaries into a validated set of turn IDs to delete. The model calls are
// injected so the prompt/parse/validate logic is testable without a model.
package contextedit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type TurnSummary struct{ ID, Role, Kind, Preview string }
type Proposal struct {
	DeleteIDs []string
	Rationale string
}
type CompleteFunc func(ctx context.Context, prompt string) (string, error)

// Propose tries local then cloud, parses the model's JSON, and keeps only
// delete_ids that exist in turns. Returns an error if no provider yields a
// parseable, non-empty proposal.
func Propose(ctx context.Context, instruction string, turns []TurnSummary, local, cloud CompleteFunc) (Proposal, error) {
	prompt := buildPrompt(instruction, turns)
	valid := make(map[string]bool, len(turns))
	for _, t := range turns {
		valid[t.ID] = true
	}
	var lastErr error
	for _, fn := range []CompleteFunc{local, cloud} {
		if fn == nil {
			continue
		}
		raw, err := fn(ctx, prompt)
		if err != nil {
			lastErr = err
			continue
		}
		p, perr := parseProposal(raw)
		if perr != nil {
			lastErr = perr
			continue
		}
		kept := p.DeleteIDs[:0]
		for _, id := range p.DeleteIDs {
			if valid[id] {
				kept = append(kept, id)
			}
		}
		p.DeleteIDs = kept
		if len(p.DeleteIDs) == 0 {
			lastErr = errors.New("no matching turns selected")
			continue
		}
		return p, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no model available")
	}
	return Proposal{}, fmt.Errorf("could not interpret instruction: %w", lastErr)
}

func buildPrompt(instruction string, turns []TurnSummary) string {
	var b strings.Builder
	b.WriteString("You curate a conversation's context. Given an instruction and a list of turns, ")
	b.WriteString("decide which turns to DELETE. Respond with ONLY a JSON object: ")
	b.WriteString(`{"delete_ids": ["<id>", ...], "rationale": "<one sentence>"}.`)
	b.WriteString(" Delete only what the instruction asks to remove; keep everything it says to retain.\n\nTurns:\n")
	for _, t := range turns {
		fmt.Fprintf(&b, "- id=%s [%s/%s] %s\n", t.ID, t.Role, t.Kind, t.Preview)
	}
	fmt.Fprintf(&b, "\nInstruction: %s\n", instruction)
	return b.String()
}

// parseProposal extracts the first JSON object from raw (models often wrap it in
// prose/markdown) and unmarshals it.
func parseProposal(raw string) (Proposal, error) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return Proposal{}, errors.New("no JSON object found")
	}
	var dto struct {
		DeleteIDs []string `json:"delete_ids"`
		Rationale string   `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &dto); err != nil {
		return Proposal{}, fmt.Errorf("bad JSON: %w", err)
	}
	return Proposal{DeleteIDs: dto.DeleteIDs, Rationale: dto.Rationale}, nil
}
