package ui

import (
	"cercano/source/clients/cli/internal/wizard"
	"cercano/source/server/pkg/agentclient"
	"context"
	"fmt"
)

// Autofilled recommendations are not pins. Only picker commits, retained in the
// resumable wizard state, become sparse overrides on the selected profile.
func wizardCloudChoices(state wizard.State, before *agentclient.CloudModelChoices) (*agentclient.CloudModelChoices, error) {
	if !wizard.ModeUsesCloud(state.LocusMode) {
		return nil, nil
	}
	choices := before.Clone()
	changed := false
	economySet := false
	economy := ""
	for _, slot := range []struct{ tier, quality string }{{"most_capable", "premium"}, {"everyday", "standard"}, {"fast_light", "economy"}, {"fast_light_text", "economy"}, {"vision", "image"}} {
		key := slot.tier + "." + wizard.SideCloud
		value, present := state.TierPicks[key]
		if !present || !state.CloudPicksEdited[key] {
			continue
		}
		changed = true
		if slot.quality == "image" {
			choices.ImageModel = value
			continue
		}
		if slot.quality == "economy" {
			if economySet && economy != value {
				return nil, fmt.Errorf("cloud fast/light slots share Economy; select one Economy model")
			}
			economySet = true
			economy = value
		}
		if value == "" {
			delete(choices.TierOverrides, slot.quality)
		} else {
			choices.TierOverrides[slot.quality] = value
		}
	}
	if !changed {
		return nil, nil
	}
	return choices, nil
}
func (wp *wizardPage) applyCloudChoices(ctx context.Context) error {
	pending, err := wizardCloudChoices(wp.state, nil)
	if err != nil || pending == nil {
		return err
	}
	profiles, active, err := wp.agent.GetCloudProfiles(ctx)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.Name == active {
			choices, err := wizardCloudChoices(wp.state, profile.Choices)
			if err != nil {
				return err
			}
			warning, err := wp.agent.SaveCloudProfile(ctx, agentclient.CloudProfileInfo{Name: profile.Name, Choices: choices})
			if warning != "" {
				wp.status = "saved cloud choices; provider unavailable: " + warning
			}
			return err
		}
	}
	return fmt.Errorf("no active profile for the wizard's cloud model choices")
}

func wizardSnapshotProfile(p agentclient.CloudProfileInfo) wizard.ProfileSnapshot {
	choices := p.Choices.Clone()
	return wizard.ProfileSnapshot{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseURL: p.BaseURL, Route: p.Route, DetailsCaptured: true, Provider: p.Provider, Region: p.Region, AWSProfile: p.AWSProfile, TierOverrides: choices.TierOverrides, ImageModel: choices.ImageModel}
}
func wizardRestoreProfile(p wizard.ProfileSnapshot) agentclient.CloudProfileInfo {
	out := agentclient.CloudProfileInfo{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseURL: p.BaseURL, Route: p.Route}
	if p.DetailsCaptured {
		out.ReplaceStructure = true
		out.Provider = p.Provider
		out.Region = p.Region
		out.AWSProfile = p.AWSProfile
		out.Choices = (&agentclient.CloudModelChoices{TierOverrides: p.TierOverrides, ImageModel: p.ImageModel}).Clone()
	}
	return out
}
