package server

import (
	"context"
	"fmt"

	"cercano/source/server/internal/skills"
	"cercano/source/server/pkg/proto"
)

// ListSkills implements proto.AgentServer — returns the catalog of available
// Agent Skills. The catalog is the embedded single source in internal/skills;
// the on-disk .agents/skills and .claude/skills trees are generated from the
// same source, so RPC listing and file discovery can't drift apart.
func (s *Server) ListSkills(ctx context.Context, req *proto.ListSkillsRequest) (*proto.ListSkillsResponse, error) {
	cat, err := skills.Catalog()
	if err != nil {
		return nil, err
	}
	protoSkills := make([]*proto.SkillInfo, len(cat))
	for i, sk := range cat {
		protoSkills[i] = &proto.SkillInfo{
			Name:        sk.Name,
			Description: sk.Description,
		}
	}
	return &proto.ListSkillsResponse{Skills: protoSkills}, nil
}

// GetSkill implements proto.AgentServer — returns the full canonical SKILL.md
// content for a specific skill.
func (s *Server) GetSkill(ctx context.Context, req *proto.GetSkillRequest) (*proto.GetSkillResponse, error) {
	cat, err := skills.Catalog()
	if err != nil {
		return nil, err
	}
	for _, sk := range cat {
		if sk.Name == req.Name {
			return &proto.GetSkillResponse{
				Name:    sk.Name,
				Content: sk.Content,
			}, nil
		}
	}
	return nil, fmt.Errorf("skill %q not found", req.Name)
}
