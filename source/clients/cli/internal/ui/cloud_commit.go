package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

type cloudCommitKind int

const (
	cloudCommitNone cloudCommitKind = iota
	cloudCommitSelect
	cloudCommitDraftEdit
	cloudCommitSave
	cloudCommitActivate
	cloudCommitDelete
	cloudCommitKey
)

type cloudCommitAction struct {
	kind  cloudCommitKind
	rowID string
	field string
	value string
}

// classifyCloudCommit maps a committed (key,value) from the Cloud Providers
// section to an action. Returns cloudCommitNone for non-cloud keys.
func classifyCloudCommit(key, value string) cloudCommitAction {
	if strings.HasPrefix(key, "cloud-row:") {
		return cloudCommitAction{kind: cloudCommitSelect, rowID: strings.TrimPrefix(key, "cloud-row:")}
	}
	switch key {
	case "cloud-name", "cloud-flavor", "cloud-backend", "cloud-base-url", "cloud-model":
		return cloudCommitAction{kind: cloudCommitDraftEdit, field: key, value: value}
	case "cloud-key":
		return cloudCommitAction{kind: cloudCommitKey, value: value}
	case "cloud-save":
		return cloudCommitAction{kind: cloudCommitSave}
	case "cloud-activate":
		return cloudCommitAction{kind: cloudCommitActivate}
	case "cloud-delete":
		return cloudCommitAction{kind: cloudCommitDelete}
	}
	return cloudCommitAction{kind: cloudCommitNone}
}

// shouldApplyModelEdit reports whether a committed cloud-section field edit is
// pushed to the server immediately instead of parking in the draft. Model
// changes on an EXISTING profile apply right away: picking a model in the
// Select and leaving the page must not silently discard the choice. New
// drafts still go through explicit save (they may lack name/flavor), and
// structural fields (name, base_url, …) keep the draft+save flow.
func shouldApplyModelEdit(field string, draftNew bool) bool {
	return field == "cloud-model" && !draftNew
}

// applyCloudDraftEdit writes one committed detail field into the draft.
func (sp *settingsPage) applyCloudDraftEdit(field, value string) {
	switch field {
	case "cloud-name":
		sp.cloudDraft.Name = value
	case "cloud-flavor":
		sp.cloudDraft.Flavor = value
	case "cloud-backend":
		sp.cloudDraft.Backend = value
	case "cloud-base-url":
		sp.cloudDraft.BaseURL = value
	case "cloud-model":
		sp.cloudDraft.Model = value
	}
}

// commitCloud executes a cloud-section action and returns the form status, an
// optional tea.Cmd, and an error. Profile mutations invalidate the cache so the
// next snapshot re-fetches.
func (sp *settingsPage) commitCloud(ca cloudCommitAction) (string, tea.Cmd, error) {
	switch ca.kind {
	case cloudCommitSelect:
		sp.selectCloudRow(ca.rowID)
		return "", nil, nil
	case cloudCommitDraftEdit:
		sp.applyCloudDraftEdit(ca.field, ca.value)
		if shouldApplyModelEdit(ca.field, sp.cloudDraftNew) && sp.agent != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			d := sp.cloudDraft
			err := sp.agent.UpsertCloudProfile(ctx, agentclient.CloudProfileInfo{
				Name: d.Name, Flavor: d.Flavor, Backend: d.Backend, BaseURL: d.BaseURL, Model: d.Model,
			})
			if err != nil {
				return "", nil, err
			}
			sp.profilesLoaded = false
			return "model applied: " + d.Model, nil, nil
		}
		return "", nil, nil
	case cloudCommitSave:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d := sp.cloudDraft
		err := sp.agent.UpsertCloudProfile(ctx, agentclient.CloudProfileInfo{
			Name: d.Name, Flavor: d.Flavor, Backend: d.Backend, BaseURL: d.BaseURL, Model: d.Model,
		})
		if err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		sp.cloudSelected = "profile:" + d.Name
		sp.cloudDraftNew = false
		return "saved " + d.Name, nil, nil
	case cloudCommitActivate:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sp.agent.SetActiveCloudProfile(ctx, sp.cloudDraft.Name); err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		return "active: " + sp.cloudDraft.Name, nil, nil
	case cloudCommitDelete:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		name := sp.cloudDraft.Name
		if err := sp.agent.RemoveCloudProfile(ctx, name); err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		sp.cloudSelected = ""
		return "deleted " + name, nil, nil
	case cloudCommitKey:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sp.agent.SetCloudProfileKey(ctx, sp.cloudDraft.Name, ca.value); err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		return "key stored for " + sp.cloudDraft.Name, nil, nil
	}
	return "", nil, nil
}
