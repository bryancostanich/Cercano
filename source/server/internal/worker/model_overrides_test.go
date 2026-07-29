package worker

import pkgcfg "cercano/source/server/pkg/config"

func workerTestModels(runtime string, tiers map[pkgcfg.Tier]string) pkgcfg.ModelsConfig {
	var m pkgcfg.ModelsConfig
	for t, id := range tiers {
		m.SetOverride(runtime, t, id)
	}
	return m
}
