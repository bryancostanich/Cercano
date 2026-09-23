// Package routingwire shares profile/task transport between settings and workers.
// It deliberately excludes credentials and runtime-owned state.
package routingwire

import (
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

func Choices(p config.CloudProfile) *proto.ProfileModelChoices {
	out := &proto.ProfileModelChoices{TierOverrides: map[string]string{}, ImageModel: p.ImageModel}
	for q, m := range p.TierOverrides {
		out.TierOverrides[string(q)] = m
	}
	return out
}
func ApplyChoices(p *config.CloudProfile, choices *proto.ProfileModelChoices) error {
	if choices == nil {
		return nil
	}
	copy := p.Clone()
	copy.TierOverrides = nil
	for q, m := range choices.TierOverrides {
		if err := copy.SetTierOverride(config.CostTier(q), m); err != nil {
			return err
		}
	}
	copy.ImageModel = choices.ImageModel
	*p = copy
	return nil
}
func Profile(p config.CloudProfile, models config.ModelProfiles) *proto.CloudProfileInfo {
	out := &proto.CloudProfileInfo{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseUrl: p.BaseURL, Route: p.Route, Provider: p.Provider, Region: p.Region, AwsProfile: p.AWSProfile, ModelChoices: Choices(p), EffectiveQualityModels: map[string]string{}}
	recommended := p.Clone()
	recommended.TierOverrides = nil
	out.RecommendedQualityModels = map[string]string{}
	for _, q := range []config.CostTier{config.CostEconomy, config.CostStandard, config.CostPremium} {
		out.RecommendedQualityModels[string(q)] = models.ResolveCloudModelForTier(recommended, q.CapabilityTier())
		out.EffectiveQualityModels[string(q)] = models.ResolveCloudModelForTier(p, q.CapabilityTier())
	}
	return out
}
func DecodeProfile(p *proto.CloudProfileInfo) (config.CloudProfile, error) {
	out := config.CloudProfile{Name: p.GetName(), Flavor: p.GetFlavor(), Backend: p.GetBackend(), BaseURL: p.GetBaseUrl(), Route: p.GetRoute(), Provider: p.GetProvider(), Region: p.GetRegion(), AWSProfile: p.GetAwsProfile()}
	err := ApplyChoices(&out, p.GetModelChoices())
	return out, err
}
func Assignments(c config.Config) *proto.RoutingAssignments {
	out := &proto.RoutingAssignments{Primary: c.ActiveCloudProfile, PrimaryBackup: c.BackupCloudProfile, PrimaryBackups: c.PrimaryBackups(), Secondary: c.SecondaryCloudProfile, SecondaryBackup: c.SecondaryBackupCloudProfile, SecondaryRedirect: string(c.SecondaryRedirect), LocalRedirect: string(c.LocalRedirect), Tasks: map[string]*proto.TaskModelAssignment{}}
	for task, a := range c.TaskAssignments {
		out.Tasks[string(task)] = &proto.TaskModelAssignment{Destination: string(a.Destination), Quality: string(a.Quality)}
	}
	return out
}
func ApplyAssignments(c *config.Config, p *proto.RoutingAssignments) {
	if p == nil {
		return
	}
	c.ActiveCloudProfile = p.Primary
	backups := p.PrimaryBackups
	if len(backups) == 0 && p.PrimaryBackup != "" {
		backups = []string{p.PrimaryBackup}
	}
	c.SetPrimaryBackups(backups)
	c.SecondaryCloudProfile, c.SecondaryBackupCloudProfile = p.Secondary, p.SecondaryBackup
	c.SecondaryRedirect, c.LocalRedirect = config.Destination(p.SecondaryRedirect), config.Destination(p.LocalRedirect)
	c.TaskAssignments = map[config.Task]config.TaskAssignment{}
	for task, a := range p.Tasks {
		c.TaskAssignments[config.Task(task)] = config.TaskAssignment{Destination: config.Destination(a.GetDestination()), Quality: config.CostTier(a.GetQuality())}
	}
}
func Snapshot(c config.Config) *proto.RoutingSnapshot {
	out := &proto.RoutingSnapshot{Assignments: Assignments(c)}
	for _, p := range c.ReferencedProfiles() {
		out.Profiles = append(out.Profiles, Profile(p, c.ModelProfiles))
	}
	return out
}

// ApplySnapshot preserves absence for compatibility with older snapshots. The
// sender's complete graph replaces legacy fields when the message is present.
func ApplySnapshot(c *config.Config, p *proto.RoutingSnapshot) error {
	if p == nil {
		return nil
	}
	next := c.Clone()
	next.CloudProfiles = nil
	for _, wire := range p.Profiles {
		profile, err := DecodeProfile(wire)
		if err != nil {
			return err
		}
		next.CloudProfiles = append(next.CloudProfiles, profile)
	}
	ApplyAssignments(&next, p.Assignments)
	if err := next.ValidateRouting(); err != nil {
		return err
	}
	*c = next
	return nil
}
