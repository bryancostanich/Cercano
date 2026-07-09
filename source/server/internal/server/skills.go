package server

import (
	"context"
	"fmt"
	"sort"

	"cercano/source/server/internal/protocols"
	"cercano/source/server/internal/skills"
	"cercano/source/server/pkg/proto"
)

type skillCatalogEntry struct {
	Name        string
	Description string
	Content     string
}

// combinedSkillCatalog returns Cercano tool skills plus protocol skills. The
// protocol entries are rendered from internal/protocols, the same source used
// by the steering block and get_protocol, so runtime RPC/MCP discovery cannot
// drift from the workflow-protocol library.
func combinedSkillCatalog() ([]skillCatalogEntry, error) {
	toolSkills, err := skills.Catalog()
	if err != nil {
		return nil, err
	}
	out := make([]skillCatalogEntry, 0, len(toolSkills)+len(protocols.Builtins()))
	seen := make(map[string]bool, len(toolSkills)+len(protocols.Builtins()))
	for _, sk := range toolSkills {
		out = append(out, skillCatalogEntry{Name: sk.Name, Description: sk.Description, Content: sk.Content})
		seen[sk.Name] = true
	}
	for _, p := range protocols.Builtins() {
		if seen[p.Name] {
			return nil, fmt.Errorf("skill catalog name collision: %s", p.Name)
		}
		out = append(out, skillCatalogEntry{Name: p.Name, Description: p.Description, Content: protocols.SkillContent(p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListSkills implements proto.AgentServer — returns the catalog of available
// Agent Skills: tool skills plus workflow protocols exposed as skills.
func (s *Server) ListSkills(ctx context.Context, req *proto.ListSkillsRequest) (*proto.ListSkillsResponse, error) {
	cat, err := combinedSkillCatalog()
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
// content for a specific skill or workflow protocol.
func (s *Server) GetSkill(ctx context.Context, req *proto.GetSkillRequest) (*proto.GetSkillResponse, error) {
	cat, err := combinedSkillCatalog()
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
