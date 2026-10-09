package builtins

import (
	"context"
	"encoding/json"
	"errors"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/managedsettings"
)

type getSharedSkillCap struct{}

// GetSharedSkill exposes assigned text without writing it into personal skills.
func GetSharedSkill() capabilities.Capability            { return getSharedSkillCap{} }
func (getSharedSkillCap) Name() string                   { return "get_shared_skill" }
func (getSharedSkillCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (getSharedSkillCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (getSharedSkillCap) Description() string {
	return "Read an organization shared skill assigned to this turn. Use its exact enterprise/... ID from the shared-skill catalog in the prompt. Returns source, version, and full text; does not execute code or grant permissions."
}
func (getSharedSkillCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","required":["id"],"properties":{"id":{"type":"string","description":"Exact enterprise/... skill ID."}},"additionalProperties":false}`)
}
func (getSharedSkillCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, errors.New("get_shared_skill: invalid arguments")
	}
	skill, ok := managedsettings.Skill(ctx, args.ID)
	if !ok {
		return nil, errors.New("get_shared_skill: skill is not assigned to this turn")
	}
	result, _ := json.Marshal(struct{ ID, Source, Version, Content string }{args.ID, "enterprise", skill.Version, skill.Content})
	return capabilities.NewTextResult(string(result)), nil
}
