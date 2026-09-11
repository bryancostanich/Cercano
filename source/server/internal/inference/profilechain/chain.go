// Package profilechain assembles a destination's independent provider failover.
package profilechain

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/pkg/config"
	"fmt"
)

type Builder func(config.CloudProfile) (inference.Provider, error)

func Build(c config.Config, d config.Destination, build Builder) (inference.Provider, error) {
	preferred, backup := c.DestinationProfiles(d)
	p, ok := c.Profile(preferred)
	if !ok {
		return nil, fmt.Errorf("%s profile unavailable", d)
	}
	defaultTier := config.TierMostCapable
	task := config.TaskChat
	if d == config.DestinationSecondary {
		task = config.TaskDispatch
	}
	defaultTier = c.TaskAssignment(task).Quality.CapabilityTier()
	modelFor := func(p config.CloudProfile) func(string) string {
		return func(tier string) string {
			if tier == "" {
				tier = string(defaultTier)
			}
			return c.ModelProfiles.ResolveCloudModelForTier(p, config.Tier(tier))
		}
	}
	p.Model = modelFor(p)("")
	primary, err := build(p)
	if err != nil {
		return nil, err
	}
	if primary == nil {
		return nil, fmt.Errorf("%s provider unavailable", d)
	}
	options := resilience.Options{PrimaryModelFor: modelFor(p)}
	if backup != "" && backup != preferred {
		if b, ok := c.Profile(backup); ok {
			b.Model = modelFor(b)("")
			if provider, err := build(b); err == nil && provider != nil {
				options.Backup = provider
				options.BackupModelFor = modelFor(b)
			}
		}
	}
	return resilience.New(primary, options), nil
}
