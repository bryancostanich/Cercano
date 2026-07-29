// Package catalogdefaults provides the single per-runtime "catalog default"
// open-model recommendation, dispatching by runtime name to the curated
// per-runtime catalogs. It is the one place allowed to import both runtime
// catalog subpackages, so server bootstrap, the setup/doctor CLIs, and the
// openmodels resolver all share exactly one default source instead of
// re-implementing the dispatch.
package catalogdefaults

import (
	"strings"

	llamaserver "cercano/source/server/internal/localruntime/llamaserver"
	mistralrs "cercano/source/server/internal/localruntime/mistralrs"
)

// ForRuntime returns the curated open-model recommendation (tier → model id)
// for a runtime at a RAM size. mistral.rs recommends its own curated chat tiers
// but keeps the embedding tier on the shared llama-served nomic (it does not
// serve embeddings); every other runtime uses the llama-server recommendation.
// The signature matches openmodels.CatalogDefaults so it can be passed directly.
func ForRuntime(runtime string, ramBytes uint64) map[string]string {
	if strings.EqualFold(runtime, "mistralrs") {
		recs := mistralrs.RecommendedOpenModels(ramBytes)
		if recs == nil {
			recs = map[string]string{}
		}
		if emb := llamaserver.RecommendedOpenModels(ramBytes)["embedding"]; emb != "" {
			recs["embedding"] = emb
		}
		return recs
	}
	return llamaserver.RecommendedOpenModels(ramBytes)
}
