package bedrock

import (
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"testing"
)

func TestFinalUsageRetainsPresence(t *testing.T) {
	zero := int32(0)
	if normalizedUsage(nil).Final || normalizedUsage(&types.TokenUsage{}).Final {
		t.Fatal("absent usage became final")
	}
	u := normalizedUsage(&types.TokenUsage{InputTokens: &zero, OutputTokens: &zero})
	if !u.Complete() {
		t.Fatalf("reported final zero lost: %+v", u)
	}
	u = normalizedUsage(&types.TokenUsage{InputTokens: &zero})
	if !u.Final || u.Complete() {
		t.Fatalf("finality fabricated output: %+v", u)
	}
}
