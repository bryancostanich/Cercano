package agentclient

import (
	"context"
	"fmt"

	"cercano/source/server/pkg/proto"
)

// SetBackupCloudProfile names the profile that serves requests when the
// active profile's provider fails; an empty name clears the backup. Mirrors
// SetActiveCloudProfile's ok/error contract.
func (c *Client) SetBackupCloudProfile(ctx context.Context, name string) error {
	resp, err := c.agent.SetBackupCloudProfile(ctx, &proto.SetBackupCloudProfileRequest{Name: name})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}
