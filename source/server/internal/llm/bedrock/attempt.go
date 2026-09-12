package bedrock

import (
	"context"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go/middleware"
)

// The SDK owns retries. Its Finalize hook downstream of Retry runs once for
// each physical attempt; the enclosing Converse invocation must not count again.
type attemptMiddleware struct {
	model         string
	streamAttempt *usage.Attempt
}

func (m *attemptMiddleware) ID() string { return "CercanoAttemptAccounting" }
func (m *attemptMiddleware) HandleFinalize(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
	a := usage.StartAttempt(ctx, "bedrock", m.model)
	out, metadata, err := next.HandleFinalize(ctx, in)
	response := llm.ChatResponse{Model: m.model, Route: &llm.ServingRoute{Provider: "bedrock", Model: m.model, Destination: "cloud"}}
	switch result := out.Result.(type) {
	case *bedrockruntime.ConverseOutput:
		if result != nil {
			response.Usage = normalizedUsage(result.Usage)
		}
	case *bedrockruntime.ConverseStreamOutput:
		if err == nil && result != nil {
			m.streamAttempt = a
			return out, metadata, err
		}
	}
	a.FinishResponse(response, err)
	return out, metadata, err
}
func (m *attemptMiddleware) option(o *bedrockruntime.Options) {
	o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
		return stack.Finalize.Insert(m, "Retry", middleware.After)
	})
}
