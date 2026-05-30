package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"cercano/source/server/internal/engine"
)

const (
	storeApp    = "cercano"
	storeUser   = "dispatch"
	storePrefix = "dispatch-"
)

// Store persists dispatch conversation history (structured ChatMessages) via session.Service.
// Uses a separate session-ID namespace from agent.ConversationStore so that the
// two cannot collide on a shared conversation_id.
type Store struct {
	svc      session.Service
	maxItems int
}

// NewStore returns a dispatch.Store backed by the given session service.
// maxItems caps the number of messages retained per conversation.
func NewStore(svc session.Service, maxItems int) *Store {
	return &Store{svc: svc, maxItems: maxItems}
}

// Save replaces the stored history for conversationID with messages.
// A no-op when conversationID is empty.
func (s *Store) Save(ctx context.Context, conversationID string, messages []engine.ChatMessage) error {
	if conversationID == "" {
		return nil
	}
	if len(messages) > s.maxItems {
		messages = messages[len(messages)-s.maxItems:]
	}
	payload, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	sess, err := s.getOrCreate(ctx, conversationID)
	if err != nil {
		return err
	}
	ev := session.NewEvent("dispatch")
	ev.Author = "dispatch"
	ev.LLMResponse.Content = genai.NewContentFromText(string(payload), genai.RoleModel)
	return s.svc.AppendEvent(ctx, sess, ev)
}

// Load returns the most recently saved history for conversationID, or nil if none.
func (s *Store) Load(ctx context.Context, conversationID string) ([]engine.ChatMessage, error) {
	if conversationID == "" {
		return nil, nil
	}
	sessionID := storePrefix + conversationID
	resp, err := s.svc.Get(ctx, &session.GetRequest{
		AppName:         storeApp,
		UserID:          storeUser,
		SessionID:       sessionID,
		NumRecentEvents: 1,
	})
	if err != nil {
		return nil, nil
	}
	events := resp.Session.Events()
	if events.Len() == 0 {
		return nil, nil
	}
	var latest string
	for e := range events.All() {
		if e.LLMResponse.Content == nil {
			continue
		}
		var text string
		for _, p := range e.LLMResponse.Content.Parts {
			text += p.Text
		}
		latest = text
	}
	if latest == "" {
		return nil, nil
	}
	var out []engine.ChatMessage
	if err := json.Unmarshal([]byte(latest), &out); err != nil {
		return nil, fmt.Errorf("corrupt dispatch history for %s: %w", conversationID, err)
	}
	return out, nil
}

func (s *Store) getOrCreate(ctx context.Context, conversationID string) (session.Session, error) {
	sessionID := storePrefix + conversationID
	resp, err := s.svc.Get(ctx, &session.GetRequest{
		AppName:   storeApp,
		UserID:    storeUser,
		SessionID: sessionID,
	})
	if err == nil && resp.Session != nil {
		return resp.Session, nil
	}
	createResp, err := s.svc.Create(ctx, &session.CreateRequest{
		AppName:   storeApp,
		UserID:    storeUser,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create dispatch session: %w", err)
	}
	return createResp.Session, nil
}
