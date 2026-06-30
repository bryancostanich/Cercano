package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

type fakeAPI struct {
	out *bedrockruntime.ConverseOutput
	err error
	in  *bedrockruntime.ConverseInput
}

func (f *fakeAPI) Converse(ctx context.Context, in *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	f.in = in
	return f.out, f.err
}
func (f *fakeAPI) ConverseStream(ctx context.Context, in *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	return nil, fmt.Errorf("not used in this test")
}

func TestChatMapsResponse(t *testing.T) {
	fake := &fakeAPI{out: &bedrockruntime.ConverseOutput{
		StopReason: types.StopReasonEndTurn,
		Output: &types.ConverseOutputMemberMessage{Value: types.Message{
			Role: types.ConversationRoleAssistant,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: "hello"},
				&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String("call_1"), Name: aws.String("get_weather"),
					Input: jsonToDocument(json.RawMessage(`{"city":"Paris"}`)),
				}},
			},
		}},
		Usage: &types.TokenUsage{InputTokens: aws.Int32(7), OutputTokens: aws.Int32(3)},
	}}
	c := &Client{api: fake, model: "anthropic.claude-x"}
	resp, err := c.Chat(context.Background(), llm.ChatRequest{
		System:   "sys",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 2 || resp.Blocks[0].Text != "hello" || resp.Blocks[1].ToolName != "get_weather" {
		t.Fatalf("blocks = %+v", resp.Blocks)
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 3 || resp.StopReason != "end_turn" {
		t.Errorf("usage/stop = %d/%d/%q", resp.InputTokens, resp.OutputTokens, resp.StopReason)
	}
	// request shape: model + system carried.
	if aws.ToString(fake.in.ModelId) != "anthropic.claude-x" || len(fake.in.System) != 1 {
		t.Errorf("request = model %q system %d", aws.ToString(fake.in.ModelId), len(fake.in.System))
	}
	if c.Name() != "bedrock" || !c.Capabilities().SupportsVision || !c.Capabilities().SupportsTools {
		t.Errorf("name/caps wrong")
	}
}

func TestChatError(t *testing.T) {
	c := &Client{api: &fakeAPI{err: fmt.Errorf("boom")}, model: "m"}
	_, err := c.Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewClientRequiresRegion(t *testing.T) {
	if _, err := NewClient(Config{Model: "m"}); err == nil {
		t.Fatal("expected error when region is empty")
	}
	c, err := NewClient(Config{Region: "us-east-1", Model: "m"})
	if err != nil || c == nil || c.Name() != "bedrock" {
		t.Fatalf("NewClient(region) → %v, %v", c, err)
	}
}
