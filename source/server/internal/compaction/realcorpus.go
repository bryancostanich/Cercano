package compaction

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/llm"
)

// realConversationJSON is the verbatim turn history of the "CERCANO - AGENT
// BEHAVIORS" development session (conversation 58ce8a3d87ba1bc8) — the very
// conversation whose unbounded, redundant compaction state motivated the
// longitudinal invariant tests. It is committed so the tests gate on the real
// pathology in CI, independent of any developer's local ~/.config database.
//
// Regenerate with:
//
//	go run ./internal/compaction/testdata/extract \
//	    -db "$HOME/.config/cercano/conversations.db" \
//	    -conv 58ce8a3d87ba1bc8 \
//	    -out ./internal/compaction/testdata/real_conversation.json
//
//go:embed testdata/real_conversation.json
var realConversationJSON []byte

// RealConversation loads the committed real-conversation fixture as an ordered
// slice of llm.Message. It panics on a malformed fixture: the file is a
// build-time asset, so a parse failure is a broken checkout, not a runtime case.
func RealConversation() []llm.Message {
	var msgs []llm.Message
	if err := json.Unmarshal(realConversationJSON, &msgs); err != nil {
		panic(fmt.Sprintf("compaction: real_conversation.json is malformed: %v", err))
	}
	return msgs
}
