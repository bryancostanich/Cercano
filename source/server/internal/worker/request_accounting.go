package worker

import (
	"cercano/source/server/internal/runner"
	"cercano/source/server/pkg/proto"
)

func (e *streamEventSink) RecordRequestAccounting(a runner.RequestAccounting) {
	e.sndr.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_RequestAccounting{RequestAccounting: &proto.RuntimeRequestAccounting{
		Model: a.Model, Provider: a.Provider, RuntimeInstanceId: a.RuntimeInstanceID,
		MessageTokens: int64(a.MessageTokens), SystemTokens: int64(a.SystemTokens), ToolSchemaTokens: int64(a.ToolSchemaTokens),
		OutputReserveTokens: int64(a.OutputReserveTokens), EstimatedRequestTokens: int64(a.EstimatedRequestTokens),
		ContextWindow: int64(a.ContextWindow), ContextWindowKnown: a.ContextWindowKnown,
	}}})
}

func unmarshalRequestAccounting(a *proto.RuntimeRequestAccounting) runner.RequestAccounting {
	return runner.RequestAccounting{
		Model: a.GetModel(), Provider: a.GetProvider(), RuntimeInstanceID: a.GetRuntimeInstanceId(),
		MessageTokens: int(a.GetMessageTokens()), SystemTokens: int(a.GetSystemTokens()), ToolSchemaTokens: int(a.GetToolSchemaTokens()),
		OutputReserveTokens: int(a.GetOutputReserveTokens()), EstimatedRequestTokens: int(a.GetEstimatedRequestTokens()),
		ContextWindow: int(a.GetContextWindow()), ContextWindowKnown: a.GetContextWindowKnown(),
	}
}
