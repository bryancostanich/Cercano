package bedrock

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

// mapStreamEvent translates one Converse stream event into an llm.StreamEvent.
// The bool is false for events we don't surface (e.g. a non-tool block start).
// It is a pure function so it can be unit-tested with synthetic events.
//
// ContentBlockStop always maps to EventToolUseStop: the tool-loop's flushTool is
// a no-op when no tool block is open, so a text block's stop is harmless. Metadata
// (usage) and MessageStop both map to EventMessageStop — the tool-loop accumulates
// stop fields with >0 guards, so the two events compose (StopReason from one,
// token usage from the other) without clobbering.
func mapStreamEvent(ev types.ConverseStreamOutput) (llm.StreamEvent, bool) {
	switch e := ev.(type) {
	case *types.ConverseStreamOutputMemberMessageStart:
		return llm.StreamEvent{Type: llm.EventMessageStart}, true
	case *types.ConverseStreamOutputMemberContentBlockStart:
		if tu, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
			return llm.StreamEvent{
				Type:      llm.EventToolUseStart,
				ToolUseID: aws.ToString(tu.Value.ToolUseId),
				ToolName:  aws.ToString(tu.Value.Name),
			}, true
		}
		return llm.StreamEvent{}, false
	case *types.ConverseStreamOutputMemberContentBlockDelta:
		switch d := e.Value.Delta.(type) {
		case *types.ContentBlockDeltaMemberText:
			return llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: d.Value}, true
		case *types.ContentBlockDeltaMemberToolUse:
			return llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: aws.ToString(d.Value.Input)}, true
		}
		return llm.StreamEvent{}, false
	case *types.ConverseStreamOutputMemberContentBlockStop:
		return llm.StreamEvent{Type: llm.EventToolUseStop}, true
	case *types.ConverseStreamOutputMemberMessageStop:
		return llm.StreamEvent{Type: llm.EventMessageStop, StopReason: string(e.Value.StopReason)}, true
	case *types.ConverseStreamOutputMemberMetadata:
		se := llm.StreamEvent{Type: llm.EventMessageStop}
		if e.Value.Usage != nil {
			se.InputTokens = int(aws.ToInt32(e.Value.Usage.InputTokens))
			se.OutputTokens = int(aws.ToInt32(e.Value.Usage.OutputTokens))
		}
		return se, true
	}
	return llm.StreamEvent{}, false
}

// streamReader pulls SDK Converse stream events off the channel and runs each
// through mapStreamEvent. Pull-based, no background goroutine.
type streamReader struct {
	es     *bedrockruntime.ConverseStreamEventStream
	events <-chan types.ConverseStreamOutput
	queued []llm.StreamEvent
}

func newStreamReader(es *bedrockruntime.ConverseStreamEventStream) *streamReader {
	return &streamReader{es: es, events: es.Events()}
}

func (r *streamReader) Next() (llm.StreamEvent, bool, error) {
	for len(r.queued) == 0 {
		ev, ok := <-r.events
		if !ok {
			if err := r.es.Err(); err != nil {
				return llm.StreamEvent{}, false, err
			}
			return llm.StreamEvent{}, false, nil
		}
		if se, emit := mapStreamEvent(ev); emit {
			r.queued = append(r.queued, se)
		}
	}
	se := r.queued[0]
	r.queued = r.queued[1:]
	return se, true, nil
}

func (r *streamReader) Close() error { return r.es.Close() }
