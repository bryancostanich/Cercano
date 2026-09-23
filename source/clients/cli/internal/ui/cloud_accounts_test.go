package ui

import (
	"cercano/source/server/pkg/agentclient"
	"testing"
)

func TestAddCloudAccountSeparatesCredentialsAndPreservesRouting(t *testing.T) {
	sp := cloudSamplePage()
	sp.profiles = append(sp.profiles, agentclient.CloudProfileInfo{Name: "chatgpt", Flavor: "responses", Route: "chatgpt"}, agentclient.CloudProfileInfo{Name: "chatgpt-2", Flavor: "responses", Route: "chatgpt"})
	sp.selectCloudRow("profile:chatgpt")
	sp.cloudDraft.apiKey = "do-not-copy"
	sp.cloudDraft.apiKeyEdited = true
	sp.cloudDraft.Choices.TierOverrides["premium"] = "custom"
	_, _, err := sp.commitCloud(classifyCloudCommit("cloud-add-account", ""))
	if err != nil {
		t.Fatal(err)
	}
	if sp.cloudDraft.Name != "chatgpt-3" || !sp.cloudDraftNew {
		t.Fatalf("draft: %+v", sp.cloudDraft)
	}
	if sp.cloudDraft.apiKey != "" || sp.cloudDraft.apiKeyEdited || len(sp.cloudDraft.Choices.TierOverrides) != 0 {
		t.Fatal("copied credentials or account choices")
	}
	_, cmd, err := sp.commitCloud(classifyCloudCommit("cloud-signin", ""))
	if err != nil {
		t.Fatal(err)
	}
	msg := cmd().(openChatGPTLoginModalMsg)
	if msg.profile != "chatgpt-3" || msg.setActive {
		t.Fatalf("wrong sign-in: %+v", msg)
	}
	sp.cloudDraft.Name = "chatgpt"
	if _, _, err := sp.commitCloud(classifyCloudCommit("cloud-signin", "")); err == nil {
		t.Fatal("new account overwrites existing account")
	}
	sp.selectCloudRow("profile:chatgpt-2")
	_, cmd, err = sp.commitCloud(classifyCloudCommit("cloud-signin", ""))
	if err != nil {
		t.Fatal(err)
	}
	if msg := cmd().(openChatGPTLoginModalMsg); msg.profile != "chatgpt-2" || msg.setActive {
		t.Fatalf("reauth: %+v", msg)
	}
}
