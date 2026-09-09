package llamaserver

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/localruntime"
	"context"
	"errors"
	"testing"
)

type refusingStartupManager struct {
	fakeRuntimeManager
	failure error
}

func (m *refusingStartupManager) Start(context.Context, localruntime.StartRequest) (*localruntime.InstanceRecord, error) {
	return nil, m.failure
}

func TestLLMProviderPreservesTypedStartupFailure(t *testing.T) {
	cause := errors.New("memory guard refused startup")
	p := NewLLMProvider(NewEngine(&refusingStartupManager{failure: cause}))
	_, err := p.Chat(context.Background(), llm.ChatRequest{Model: "glm-local"})
	var startup *llm.LocalStartupError
	if !errors.As(err, &startup) || !errors.Is(err, cause) || startup.Model != "glm-local" {
		t.Fatalf("Chat startup type lost: %v", err)
	}
	stream, err := p.StreamChat(context.Background(), llm.ChatRequest{Model: "glm-local"})
	if stream != nil || !errors.As(err, &startup) || !errors.Is(err, cause) {
		t.Fatalf("StreamChat startup type lost: %v", err)
	}
}
