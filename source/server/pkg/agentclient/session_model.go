package agentclient

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/pkg/proto"
)

type SessionModelRoute struct {
	Profile string `json:"profile"`
	Model   string `json:"model"`
}
type SessionModelProfile struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"configured_model"`
}
type SessionModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type SessionModelStatus struct {
	Models   []SessionModelInfo    `json:"models"`
	Override *SessionModelRoute    `json:"override"`
	Profiles []SessionModelProfile `json:"profiles"`
	Note     string                `json:"note"`
}

func (c *Client) SessionModel(ctx context.Context, conversationID, workDir, action, profile, model string) (SessionModelStatus, error) {
	response, err := c.agent.SessionModel(ctx, &proto.SessionModelRequest{ConversationId: conversationID, WorkDir: workDir, Action: action, Profile: profile, Model: model})
	if err != nil {
		return SessionModelStatus{}, err
	}
	if response.GetError() != "" {
		return SessionModelStatus{}, fmt.Errorf("%s", response.GetError())
	}
	var result SessionModelStatus
	err = json.Unmarshal(response.GetResultJson(), &result)
	return result, err
}
