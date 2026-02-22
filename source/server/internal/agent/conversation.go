package agent

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const MaxChatResponseLen = 2000

// Future enhancement: consider LLM-based summarization for more sophisticated
// compaction of conversation history, especially for long chat responses.

// CompactResponse creates a storage-efficient summary of a response.
// Chat responses (no FileChanges): keep text, truncate if long.
// Coding responses (has FileChanges): compact to "[Code generated: ACTION path, ...]"
func CompactResponse(resp *Response) string {
	if len(resp.FileChanges) > 0 {
		parts := make([]string, len(resp.FileChanges))
		for i, fc := range resp.FileChanges {
			parts[i] = fmt.Sprintf("%s %s", fc.Action, fc.Path)
		}
		return fmt.Sprintf("[Code generated: %s]", strings.Join(parts, ", "))
	}
	if len(resp.Output) > MaxChatResponseLen {
		return resp.Output[:MaxChatResponseLen] + "..."
	}
	return resp.Output
}

// ConversationStore manages multi-turn conversation history using the shared SessionService.
type ConversationStore struct {
	svc      session.Service
	maxTurns int
}

// NewConversationStore creates a ConversationStore backed by the given session service.
func NewConversationStore(svc session.Service, maxTurns int) *ConversationStore {
	return &ConversationStore{
		svc:      svc,
		maxTurns: maxTurns,
	}
}

// getOrCreateSession retrieves an existing session or creates a new one.
func (cs *ConversationStore) getOrCreateSession(ctx context.Context, conversationID string) (session.Session, error) {
	sessionID := "conv-" + conversationID

	resp, err := cs.svc.Get(ctx, &session.GetRequest{
		AppName:   "cercano",
		UserID:    "conversation",
		SessionID: sessionID,
	})
	if err == nil && resp.Session != nil {
		return resp.Session, nil
	}

	createResp, err := cs.svc.Create(ctx, &session.CreateRequest{
		AppName:   "cercano",
		UserID:    "conversation",
		SessionID: sessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation session: %w", err)
	}
	return createResp.Session, nil
}

// AppendTurn stores a user message + assistant response as two events in the conversation session.
func (cs *ConversationStore) AppendTurn(ctx context.Context, conversationID, userMsg, assistantResp string) error {
	if conversationID == "" {
		return nil
	}

	sess, err := cs.getOrCreateSession(ctx, conversationID)
	if err != nil {
		return err
	}

	// Append user event
	userEvent := session.NewEvent("conv")
	userEvent.Author = "user"
	userEvent.LLMResponse.Content = genai.NewContentFromText(userMsg, genai.RoleUser)
	if err := cs.svc.AppendEvent(ctx, sess, userEvent); err != nil {
		return fmt.Errorf("failed to append user event: %w", err)
	}

	// Append assistant event
	assistantEvent := session.NewEvent("conv")
	assistantEvent.Author = "assistant"
	assistantEvent.LLMResponse.Content = genai.NewContentFromText(assistantResp, genai.RoleModel)
	if err := cs.svc.AppendEvent(ctx, sess, assistantEvent); err != nil {
		return fmt.Errorf("failed to append assistant event: %w", err)
	}

	return nil
}

// LoadHistory retrieves the last N turns, formatted as text for prompt injection.
// Returns "" if no history exists or conversationID is empty.
func (cs *ConversationStore) LoadHistory(ctx context.Context, conversationID string) (string, error) {
	if conversationID == "" {
		return "", nil
	}

	sessionID := "conv-" + conversationID
	resp, err := cs.svc.Get(ctx, &session.GetRequest{
		AppName:         "cercano",
		UserID:          "conversation",
		SessionID:       sessionID,
		NumRecentEvents: cs.maxTurns * 2, // 2 events per turn (user + assistant)
	})
	if err != nil {
		// No session yet — no history
		return "", nil
	}

	events := resp.Session.Events()
	if events.Len() == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("--- Conversation History ---\n")

	for event := range events.All() {
		if event.LLMResponse.Content == nil {
			continue
		}
		var text string
		for _, part := range event.LLMResponse.Content.Parts {
			text += part.Text
		}
		if text == "" {
			continue
		}

		switch event.Author {
		case "user":
			sb.WriteString("User: ")
			sb.WriteString(text)
			sb.WriteString("\n")
		case "assistant":
			sb.WriteString("Assistant: ")
			sb.WriteString(text)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("--- End History ---\n")
	return sb.String(), nil
}
