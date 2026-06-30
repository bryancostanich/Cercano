package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

func TestMapStreamEvent(t *testing.T) {
	cases := []struct {
		name  string
		in    types.ConverseStreamOutput
		emit  bool
		etype llm.StreamEventType
	}{
		{"start", &types.ConverseStreamOutputMemberMessageStart{Value: types.MessageStartEvent{Role: types.ConversationRoleAssistant}}, true, llm.EventMessageStart},
		{"text", &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberText{Value: "hi"}}}, true, llm.EventTextDelta},
		{"toolstart", &types.ConverseStreamOutputMemberContentBlockStart{Value: types.ContentBlockStartEvent{Start: &types.ContentBlockStartMemberToolUse{Value: types.ToolUseBlockStart{ToolUseId: aws.String("call_1"), Name: aws.String("get_weather")}}}}, true, llm.EventToolUseStart},
		{"toolinput", &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberToolUse{Value: types.ToolUseBlockDelta{Input: aws.String(`{"city":`)}}}}, true, llm.EventToolUseInputDelta},
		{"blockstop", &types.ConverseStreamOutputMemberContentBlockStop{Value: types.ContentBlockStopEvent{}}, true, llm.EventToolUseStop},
		{"msgstop", &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn}}, true, llm.EventMessageStop},
		{"metadata", &types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{Usage: &types.TokenUsage{InputTokens: aws.Int32(9), OutputTokens: aws.Int32(4)}}}, true, llm.EventMessageStop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, emit := mapStreamEvent(tc.in)
			if emit != tc.emit || (emit && ev.Type != tc.etype) {
				t.Fatalf("got (%+v, %v), want type %v emit %v", ev, emit, tc.etype, tc.emit)
			}
		})
	}

	// field-level checks
	ev, _ := mapStreamEvent(&types.ConverseStreamOutputMemberContentBlockStart{Value: types.ContentBlockStartEvent{Start: &types.ContentBlockStartMemberToolUse{Value: types.ToolUseBlockStart{ToolUseId: aws.String("call_1"), Name: aws.String("get_weather")}}}})
	if ev.ToolUseID != "call_1" || ev.ToolName != "get_weather" {
		t.Errorf("tool start fields = %+v", ev)
	}
	ev, _ = mapStreamEvent(&types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberText{Value: "hi"}}})
	if ev.TextDelta != "hi" {
		t.Errorf("text delta = %q", ev.TextDelta)
	}
	ev, _ = mapStreamEvent(&types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{Usage: &types.TokenUsage{InputTokens: aws.Int32(9), OutputTokens: aws.Int32(4)}}})
	if ev.InputTokens != 9 || ev.OutputTokens != 4 {
		t.Errorf("metadata usage = %d/%d", ev.InputTokens, ev.OutputTokens)
	}

	// a non-tool content block start emits nothing
	if _, emit := mapStreamEvent(&types.ConverseStreamOutputMemberContentBlockStart{Value: types.ContentBlockStartEvent{}}); emit {
		t.Error("non-tool block start should not emit")
	}
}
