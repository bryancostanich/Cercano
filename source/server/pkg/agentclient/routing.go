package agentclient

import (
	"cercano/source/server/pkg/proto"
	"context"
	"fmt"
)

type CloudModelChoices struct {
	TierOverrides map[string]string
	ImageModel    string
}

func (c *CloudModelChoices) Clone() *CloudModelChoices {
	if c == nil {
		return &CloudModelChoices{TierOverrides: map[string]string{}}
	}
	out := &CloudModelChoices{TierOverrides: copyStringMap(c.TierOverrides), ImageModel: c.ImageModel}
	if out.TierOverrides == nil {
		out.TierOverrides = map[string]string{}
	}
	return out
}

type TaskAssignment struct{ Destination, Quality string }
type RoutingAssignments struct {
	SecondaryRedirect, LocalRedirect                   string
	Primary, PrimaryBackup, Secondary, SecondaryBackup string
	Tasks                                              map[string]TaskAssignment
}

func (a *RoutingAssignments) Clone() *RoutingAssignments {
	if a == nil {
		return &RoutingAssignments{Tasks: map[string]TaskAssignment{}}
	}
	c := *a
	c.Tasks = map[string]TaskAssignment{}
	for k, v := range a.Tasks {
		c.Tasks[k] = v
	}
	return &c
}
func copyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range src {
		out[k] = v
	}
	return out
}
func choicesToProto(c *CloudModelChoices) *proto.ProfileModelChoices {
	if c == nil {
		return nil
	}
	return &proto.ProfileModelChoices{TierOverrides: copyStringMap(c.TierOverrides), ImageModel: c.ImageModel}
}
func choicesFromProto(c *proto.ProfileModelChoices) *CloudModelChoices {
	if c == nil {
		return nil
	}
	return &CloudModelChoices{TierOverrides: copyStringMap(c.TierOverrides), ImageModel: c.ImageModel}
}
func assignmentsToProto(a *RoutingAssignments) *proto.RoutingAssignments {
	if a == nil {
		return nil
	}
	out := &proto.RoutingAssignments{Primary: a.Primary, PrimaryBackup: a.PrimaryBackup, Secondary: a.Secondary, SecondaryBackup: a.SecondaryBackup, SecondaryRedirect: a.SecondaryRedirect, LocalRedirect: a.LocalRedirect, Tasks: map[string]*proto.TaskModelAssignment{}}
	for k, v := range a.Tasks {
		out.Tasks[k] = &proto.TaskModelAssignment{Destination: v.Destination, Quality: v.Quality}
	}
	return out
}
func assignmentsFromProto(a *proto.RoutingAssignments) *RoutingAssignments {
	if a == nil {
		return nil
	}
	out := &RoutingAssignments{Primary: a.Primary, PrimaryBackup: a.PrimaryBackup, Secondary: a.Secondary, SecondaryBackup: a.SecondaryBackup, SecondaryRedirect: a.SecondaryRedirect, LocalRedirect: a.LocalRedirect, Tasks: map[string]TaskAssignment{}}
	for k, v := range a.Tasks {
		out.Tasks[k] = TaskAssignment{Destination: v.GetDestination(), Quality: v.GetQuality()}
	}
	return out
}

// UpdateRoutingAssignments returns a nonfatal availability warning separately
// from validation/transport failure. A warning means the draft was saved.
func (c *Client) UpdateRoutingAssignments(ctx context.Context, a *RoutingAssignments) (string, error) {
	resp, err := c.agent.UpdateRoutingAssignments(ctx, &proto.UpdateRoutingAssignmentsRequest{Assignments: assignmentsToProto(a)})
	if err != nil {
		return "", err
	}
	if !resp.GetOk() {
		return "", fmt.Errorf("%s", resp.GetError())
	}
	return resp.GetWarning(), nil
}

func nonemptyProfileField(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func profileStructureToProto(p CloudProfileInfo) *proto.CloudProfileStructure {
	if !p.ReplaceStructure {
		return nil
	}
	return &proto.CloudProfileStructure{Flavor: p.Flavor, Backend: p.Backend, BaseUrl: p.BaseURL, Route: p.Route, Provider: p.Provider, Region: p.Region, AwsProfile: p.AWSProfile}
}
