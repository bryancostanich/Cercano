// Command extract dumps one conversation's turns from a Cercano conversations.db
// into a committed JSON fixture ([]llm.Message) for the compaction invariant and
// fuzz tests. It is a developer tool, run by hand to (re)generate the fixture; it
// is not part of any build or test path.
//
// Usage:
//
//	go run ./internal/compaction/testdata/extract \
//	    -db "$HOME/.config/cercano/conversations.db" \
//	    -conv 58ce8a3d87ba1bc8 \
//	    -out ./internal/compaction/testdata/real_conversation.json
//
// The on-disk turns.content_json column is exactly json.Marshal([]llm.Block),
// so each turn round-trips into an llm.Message by pairing role + blocks with no
// lossy field mapping.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"cercano/source/server/internal/llm"
)

func main() {
	dbPath := flag.String("db", "", "path to conversations.db")
	conv := flag.String("conv", "", "conversation id to extract")
	out := flag.String("out", "", "output fixture path")
	flag.Parse()

	if *dbPath == "" || *conv == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: extract -db PATH -conv ID -out PATH")
		os.Exit(2)
	}

	db, err := sql.Open("sqlite", "file:"+*dbPath+"?mode=ro")
	must(err)
	defer db.Close()

	rows, err := db.Query(
		`SELECT role, content, content_json FROM turns
		 WHERE conversation_id = ? ORDER BY created_at, id`, *conv)
	must(err)
	defer rows.Close()

	var msgs []llm.Message
	var skipped int
	for rows.Next() {
		var role, content, cj string
		must(rows.Scan(&role, &content, &cj))

		var blocks []llm.Block
		if cj != "" {
			if err := json.Unmarshal([]byte(cj), &blocks); err != nil {
				// A turn whose content_json is malformed falls back to a plain
				// text block so the transcript stays complete and ordered.
				blocks = []llm.Block{{Type: llm.BlockText, Text: content}}
				skipped++
			}
		} else {
			blocks = []llm.Block{{Type: llm.BlockText, Text: content}}
		}
		msgs = append(msgs, llm.Message{Role: llm.Role(role), Blocks: blocks})
	}
	must(rows.Err())

	data, err := json.MarshalIndent(msgs, "", " ")
	must(err)
	must(os.WriteFile(*out, data, 0o644))

	fmt.Fprintf(os.Stderr, "wrote %d messages (%d content_json fallbacks) -> %s\n",
		len(msgs), skipped, *out)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
