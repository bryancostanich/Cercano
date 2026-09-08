package contextmeter

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// cl100kBase is the cl100k_base BPE merge table, embedded so tokenization
// never depends on network reachability or on a writable cache directory.
//
// Upstream tiktoken-go resolves encodings by HTTP GET against
// openaipublic.blob.core.windows.net, memoised into $TIKTOKEN_CACHE_DIR (or
// $TMPDIR/data-gym-cache when unset). Both legs of that are fragile for us:
// a cold container or CI runner has no cache, and macOS purges TMPDIR on an
// age basis. When the fetch fails, Default() silently degrades to
// fallbackTokenizer — char/4 arithmetic — and every budget decision in the
// process quietly gets worse without surfacing an error. That is especially
// bad for high-entropy content: base64 hashes and minified JSON tokenize
// near 1.8 chars/token, where char/4 reports under half the true count.
//
// Embedding costs ~1.7 MB of binary and removes the failure class outright.
//
//go:embed cl100k_base.tiktoken
var cl100kBase string

// cl100kBaseURL is the blob path tiktoken-go requests for cl100k_base. We
// match on it rather than serving every request from the embedded table so
// that asking for an encoding we have not vendored fails loudly instead of
// being silently mis-decoded as cl100k.
const cl100kBaseURL = "https://openaipublic.blob.core.windows.net/encodings/cl100k_base.tiktoken"

// embeddedBpeLoader serves the vendored cl100k_base table and refuses
// anything else. It satisfies tiktoken.BpeLoader.
type embeddedBpeLoader struct{}

func (embeddedBpeLoader) LoadTiktokenBpe(tiktokenBpeFile string) (map[string]int, error) {
	if tiktokenBpeFile != cl100kBaseURL {
		return nil, fmt.Errorf("contextmeter: no embedded BPE table for %q (only cl100k_base is vendored)", tiktokenBpeFile)
	}
	return parseTiktokenBpe(cl100kBase)
}

// parseTiktokenBpe decodes the "<base64 token> <rank>" line format used by
// the published .tiktoken files. Mirrors upstream loadTiktokenBpe, minus the
// fetch and cache layers.
func parseTiktokenBpe(contents string) (map[string]int, error) {
	ranks := make(map[string]int, 100_256)
	for i, line := range strings.Split(contents, "\n") {
		if line == "" {
			continue
		}
		token, rank, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("contextmeter: malformed BPE line %d: %q", i+1, line)
		}
		decoded, err := base64.StdEncoding.DecodeString(token)
		if err != nil {
			return nil, fmt.Errorf("contextmeter: BPE line %d: decode token: %w", i+1, err)
		}
		n, err := strconv.Atoi(rank)
		if err != nil {
			return nil, fmt.Errorf("contextmeter: BPE line %d: parse rank: %w", i+1, err)
		}
		ranks[string(decoded)] = n
	}
	if len(ranks) == 0 {
		return nil, fmt.Errorf("contextmeter: embedded BPE table is empty")
	}
	return ranks, nil
}

// installEmbeddedBpeLoader points tiktoken-go at the vendored table.
//
// SetBpeLoader mutates a package-level global in tiktoken-go, so this must
// run before the first GetEncoding call. Default() does that inside its
// sync.Once, which is the only path in this package that constructs an
// encoding.
func installEmbeddedBpeLoader() {
	tiktoken.SetBpeLoader(embeddedBpeLoader{})
}
