package mcp

import (
	"context"
	"testing"

	"cercano/source/server/internal/web"
	wire "cercano/source/server/pkg/proto"

	"google.golang.org/protobuf/proto"
)

func TestResearchQueryRPCPolicyDoesNotLeakOrLoseUsage(t *testing.T) {
	client := &mockAgentClient{processResp: &wire.ProcessRequestResponse{Output: "1. query", InputTokens: 2, OutputTokens: 3}}
	model := &grpcModelCallerWithTokens{client: client, modelOverride: "chosen-model"}
	pipeline := web.NewResearchPipeline(model, nil, nil)
	if _, err := pipeline.CraftQueries(context.Background(), "question"); err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(client.lastRequest)
	if err != nil {
		t.Fatal(err)
	}
	var query wire.ProcessRequestRequest
	if err := proto.Unmarshal(data, &query); err != nil {
		t.Fatal(err)
	}
	if !query.GetDisableThinking() || query.GetRoutingTask() != "research" || query.GetCoproc() || query.GetModelOverride() != "chosen-model" {
		t.Fatalf("query=%v", &query)
	}
	if _, err := model.Call(context.Background(), "synthesis"); err != nil {
		t.Fatal(err)
	}
	if client.lastRequest.GetDisableThinking() {
		t.Fatal("query policy leaked to ordinary model call")
	}
	if model.totalCalls != 2 || model.totalIn != 4 || model.totalOut != 6 {
		t.Fatalf("usage calls=%d in=%d out=%d", model.totalCalls, model.totalIn, model.totalOut)
	}
}
