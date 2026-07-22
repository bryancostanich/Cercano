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
	cloudCommitBackup
	cloudCommitDelete
	cloudCommitKey
	cloudCommitSignIn
	cloudCommitSignInClaude
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
	case "cloud-backup":
		return cloudCommitAction{kind: cloudCommitBackup}
	case "cloud-delete":
		return cloudCommitAction{kind: cloudCommitDelete}
	case "cloud-signin":
		return cloudCommitAction{kind: cloudCommitSignIn}
	case "cloud-signin-claude":
		return cloudCommitAction{kind: cloudCommitSignInClaude}
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

// cloudCommitNeedsAgent reports whether executing the action reaches the
// agent over gRPC. Row selection and plain draft edits are local; a draft
// model edit on an existing profile pushes immediately (shouldApplyModelEdit)
// and so needs the agent too.
func cloudCommitNeedsAgent(ca cloudCommitAction, draftNew bool) bool {
	switch ca.kind {
	case cloudCommitSave, cloudCommitActivate, cloudCommitBackup,
		cloudCommitDelete, cloudCommitKey, cloudCommitSignIn, cloudCommitSignInClaude:
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
				Name: d.Name, Flavor: d.Flavor, Backend: d.Backend, Route: d.Route, BaseURL: d.BaseURL, Model: d.Model,
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
			Name: d.Name, Flavor: d.Flavor, Backend: d.Backend, Route: d.Route, BaseURL: d.BaseURL, Model: d.Model,
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
		return "primary: " + sp.cloudDraft.Name, nil, nil
	case cloudCommitBackup:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Toggle: pressing the button on the profile that already IS the
		// backup clears it (the button reads "clear backup" in that state).
		name := sp.cloudDraft.Name
		if sp.cloudView.Backup == name {
			name = ""
		}
		if err := sp.agent.SetBackupCloudProfile(ctx, name); err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		if name == "" {
			return "backup cleared", nil, nil
		}
		return "backup: " + name, nil, nil
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
