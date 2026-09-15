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
	cloudCommitDelete
	cloudCommitKey
	cloudCommitSignIn
	cloudCommitSignInClaude
	cloudCommitDiscard
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
	if key == "cloud-discard" {
		return cloudCommitAction{kind: cloudCommitDiscard}
	}
	if strings.HasPrefix(key, "cloud-quality-") || strings.HasPrefix(key, "cloud-custom-") || key == "cloud-image" {
		return cloudCommitAction{kind: cloudCommitDraftEdit, field: key, value: value}
	}
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
	case "cloud-delete":
		return cloudCommitAction{kind: cloudCommitDelete}
	case "cloud-signin":
		return cloudCommitAction{kind: cloudCommitSignIn}
	case "cloud-signin-claude":
		return cloudCommitAction{kind: cloudCommitSignInClaude}
	}
	return cloudCommitAction{kind: cloudCommitNone}
}

// All profile model edits remain drafts until explicit Save.
func shouldApplyModelEdit(field string, draftNew bool) bool {
	return false
}

// applyCloudDraftEdit writes one committed detail field into the draft.
func (sp *settingsPage) applyCloudDraftEdit(field, value string) {
	sp.cloudDirty = true
	if sp.applyCloudChoice(field, value) {
		return
	}
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

// cloudCommitNeedsAgent reports whether executing the action reaches the
// agent over gRPC. Row selection and all draft edits stay local. Explicit
// profile saves, deletion and sign-in actions reach the agent. Credentials
// remain in the draft until explicit Save.
func cloudCommitNeedsAgent(ca cloudCommitAction, draftNew bool) bool {
	switch ca.kind {
	case cloudCommitSave,
		cloudCommitDelete, cloudCommitSignIn, cloudCommitSignInClaude:
		return true
	case cloudCommitDraftEdit:
		return shouldApplyModelEdit(ca.field, draftNew)
	}
	return false
}

// commitCloud executes a cloud-section action and returns the form status, an
// optional tea.Cmd, and an error. Profile mutations invalidate the cache so the
// next snapshot re-fetches.
func (sp *settingsPage) commitCloud(ca cloudCommitAction) (string, tea.Cmd, error) {
	// Fail fast while the connection is down: these RPCs block the update
	// loop for their full deadline and then fail anyway. The reconnect
	// watcher already knows the agent is restarting — say so instead.
	if cloudCommitNeedsAgent(ca, sp.cloudDraftNew) && sp.agent != nil &&
		sp.agent.State() != agentclient.ConnStateConnected {
		return "agent reconnecting — retry in a moment", nil, nil
	}
	switch ca.kind {
	case cloudCommitDiscard:
		sp.selectCloudRow(sp.cloudSelected)
		return "discarded profile draft", nil, nil
	case cloudCommitSelect:
		if sp.cloudDirty {
			sp.settingsPendingLeave = ca.rowID
			sp.settingsNavigationPrompt = true
			return "Unsaved profile edits. Discard and leave? y/n", nil, nil
		}
		sp.selectCloudRow(ca.rowID)
		return "", nil, nil
	case cloudCommitDraftEdit:
		sp.applyCloudDraftEdit(ca.field, ca.value)

		return "", nil, nil
	case cloudCommitSave:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d := sp.cloudDraft
		warning, err := sp.agent.SaveCloudProfile(ctx, agentclient.CloudProfileInfo{
			ReplaceStructure: true, Name: d.Name, Flavor: d.Flavor, Backend: d.Backend, Route: d.Route, BaseURL: d.BaseURL, Choices: d.Choices, Provider: d.Provider, Region: d.Region, AWSProfile: d.AWSProfile,
		})
		if err != nil {
			return "", nil, err
		}
		// Profile creation must succeed before its credential can be stored.
		// Keep the pending key and dirty state on failure so Save can retry.
		sp.profilesLoaded = false
		if d.apiKeyEdited {
			if err := sp.agent.SetCloudProfileKey(ctx, d.Name, d.apiKey); err != nil {
				return "", nil, err
			}
		}
		sp.cloudDraft.apiKey = ""
		sp.cloudDraft.apiKeyEdited = false
		sp.cloudSelected = "profile:" + d.Name
		sp.cloudDraftNew = false
		sp.cloudDirty = false
		if warning != "" {
			return "saved " + d.Name + "; provider unavailable: " + warning, nil, nil
		}
		return "saved " + d.Name, nil, nil
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
	case cloudCommitSignIn:
		// The device-code sign-in runs in a modal owned by the root model. The
		// profile name is owned by the server (canonical "chatgpt"), so send an
		// empty name — this way /config and the wizard produce the same single
		// profile instead of one named after whichever row launched it.
		model := strings.TrimSpace(sp.cloudDraft.Model)
		return "starting ChatGPT sign-in…", func() tea.Msg {
			return openChatGPTLoginModalMsg{profile: "", model: model, setActive: true}
		}, nil
	case cloudCommitSignInClaude:
		// Settings sign-in belongs to the selected provider/profile row. Passing
		// the draft name stores the subscription token in that profile's secret
		// slot, so activating "anthropic" later builds the same profile the user
		// signed into. The wizard still uses an empty profile to request the
		// canonical server-owned default.
		profile := strings.TrimSpace(sp.cloudDraft.Name)
		claudeModel := strings.TrimSpace(sp.cloudDraft.Model)
		return "starting Claude sign-in…", func() tea.Msg {
			return openClaudeLoginModalMsg{profile: profile, model: claudeModel, setActive: true}
		}, nil
	case cloudCommitKey:
		sp.cloudDraft.apiKey = ca.value
		sp.cloudDraft.apiKeyEdited = true
		sp.cloudDirty = true
		return "key edited — Save to apply", nil, nil

	}
	return "", nil, nil
}
