package ollama

import (
	api "github.com/ollama/ollama/api"
	"testing"
)

func TestUsageFinalityRequiresDoneAndReportedCounts(t *testing.T) {
	var response api.ChatResponse
	response.PromptEvalCount = 11
	response.EvalCount = 7
	if normalizedUsage(response).Final {
		t.Fatal("running usage marked final")
	}
	response.Done = true
	if !normalizedUsage(response).Complete() {
		t.Fatal("done usage not final")
	}
	response.EvalCount = 0
	if u := normalizedUsage(response); !u.Final || u.Complete() || u.Output.Known {
		t.Fatalf("ambiguous zero fabricated: %+v", u)
	}
	response.PromptEvalCount = 0
	if normalizedUsage(response).Final {
		t.Fatal("done alone fabricated usage evidence")
	}
}
