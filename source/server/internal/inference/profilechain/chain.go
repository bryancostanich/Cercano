// Package profilechain assembles a destination's independent provider failover.
package profilechain

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
	"fmt"
)

type Builder func(config.CloudProfile) (inference.Provider, error)

func Build(c config.Config, d config.Destination, build Builder, events ...func(resilience.Event)) (inference.Provider, error) {
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
	primary, primaryErr := build(p)
	if primary == nil && primaryErr == nil {
		primaryErr = fmt.Errorf("%s provider unavailable", d)
	}
	options := resilience.Options{PrimaryModelFor: modelFor(p)}
	if len(events) > 0 {
		options.OnEvent = events[0]
	}
	if backup != "" && backup != preferred {
		if b, ok := c.Profile(backup); ok {
			b.Model = modelFor(b)("")
			provider, err := build(b)
			if err != nil {
				switch llm.ClassOf(err) {
				case llm.ErrAuth, llm.ErrCredential, llm.ErrLoginRequired, llm.ErrPermission:
					provider = &unavailableProvider{err: err}
				}
			}
			if provider != nil {
				options.Backup = &routeProvider{Provider: provider, profile: b.Name, destination: string(d)}
				options.BackupModelFor = modelFor(b)
				options.BackupLabel = b.Name
			}
		}
	}
	if primaryErr != nil {
		class := llm.ClassOf(primaryErr)
		actionable := class == llm.ErrLoginRequired || class == llm.ErrCredential || class == llm.ErrPermission
		if options.Backup == nil && !actionable {
			return nil, primaryErr
		}
		primary = &unavailableProvider{err: primaryErr}
		options.PrimaryUnavailable = !actionable
		options.PrimaryBlocked = actionable
	}
	primary = &routeProvider{Provider: primary, profile: p.Name, destination: string(d)}
	return resilience.New(primary, options), nil
}
