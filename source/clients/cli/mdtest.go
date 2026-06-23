package main

import (
	"fmt"
	"os"
)

// markdownSample exercises every element the viewport renders: heading levels,
// inline emphasis, inline + fenced code (for chroma), nested lists, blockquote,
// links, a rule, and two tables — one narrow, one wide enough to trigger the
// responsive renderer's column dropping on an 80-col terminal.
const markdownSample = "# Cercano Markdown Render Test\n" +
	"\n" +
	"This is a **bold** word, an *italic* word, and some `inline code`.\n" +
	"Here is a [link](https://example.com) in a sentence.\n" +
	"\n" +
	"## Lists\n" +
	"\n" +
	"- First bullet\n" +
	"- Second bullet\n" +
	"  - Nested bullet\n" +
	"  - Another nested\n" +
	"- Third bullet\n" +
	"\n" +
	"1. First numbered\n" +
	"2. Second numbered\n" +
	"3. Third numbered\n" +
	"\n" +
	"### Blockquote\n" +
	"\n" +
	"> The sim output is TRUTH. Your mental model is a hypothesis.\n" +
	"\n" +
	"### Fenced code (Go)\n" +
	"\n" +
	"```go\n" +
	"package main\n" +
	"\n" +
	"import \"fmt\"\n" +
	"\n" +
	"func main() {\n" +
	"\tfmt.Println(\"hello, cercano\")\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"### Fenced code (JSON)\n" +
	"\n" +
	"```json\n" +
	"{\n" +
	"  \"model\": \"qwen3-coder\",\n" +
	"  \"escalated\": false,\n" +
	"  \"confidence\": 1.0\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"## Tables\n" +
	"\n" +
	"A narrow table:\n" +
	"\n" +
	"| Tier | Action |\n" +
	"| --- | --- |\n" +
	"| R | read |\n" +
	"| W | write |\n" +
	"| X | delete |\n" +
	"\n" +
	"A wide table (should drop/transpose on a narrow terminal):\n" +
	"\n" +
	"| Tool | Tier | Args | What it does |\n" +
	"| --- | --- | --- | --- |\n" +
	"| Read | R | path | Read a file from disk into the context |\n" +
	"| Bash | W | cmd | Run a shell command with a 16 KiB output cap |\n" +
	"| git_push | X | remote, branch | Push commits, force-with-lease when forced |\n" +
	"\n" +
	"---\n" +
	"\n" +
	"That's the end of the sample.\n"

// loadMdTestDoc returns the markdown to pre-load for `--mdtest`. With no path it
// returns the built-in sample; otherwise it reads the given file. Exits with
// usage code 2 if the file can't be read.
func loadMdTestDoc(path string) string {
	if path == "" {
		return markdownSample
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cercano: could not read %s: %v\n", path, err)
		os.Exit(2)
	}
	return string(b)
}
