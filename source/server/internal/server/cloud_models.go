package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cercano/source/server/pkg/proto"
)

// ListCloudProfileModels implements proto.AgentServer — hits the named
// profile's /v1/models endpoint (through Meridian's OAuth for route=meridian,
// or directly with the profile's keychain-stored API key for direct routes)
// and returns the catalog for the settings UI. Empty models + populated
// error means "fetch failed; render the curated fallback list instead."
func (s *Server) ListCloudProfileModels(ctx context.Context, req *proto.ListCloudProfileModelsRequest) (*proto.ListCloudProfileModelsResponse, error) {
	name := req.GetProfileName()
	cfg := s.cfgSvc.Get()
	if name == "" {
		name = cfg.ActiveCloudProfile
	}
	p, ok := profileByName(cfg.CloudProfiles, name)
	if !ok {
		return &proto.ListCloudProfileModelsResponse{Error: fmt.Sprintf("no profile %q", name)}, nil
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		return &proto.ListCloudProfileModelsResponse{Error: "profile has no base_url"}, nil
	}
	// Only Anthropic-style profiles surface a useful /v1/models list today.
	// chat_completions profiles use OpenAI-shape catalogs which the settings
	// UI doesn't consume yet — return an empty list so the client falls
	// back cleanly.
	if p.Flavor != "messages" {
		return &proto.ListCloudProfileModelsResponse{Error: fmt.Sprintf("flavor %q not supported for model catalog", p.Flavor)}, nil
	}

	url := base + "/v1/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &proto.ListCloudProfileModelsResponse{Error: err.Error()}, nil
	}
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	// Auth: carry the profile's API key from the keychain. Subscription
	// profiles have an empty base_url and are short-circuited above onto the
	// curated fallback list, so they never reach this fetch.
	if st := s.cfgSvc.Secrets(); st != nil {
		if key, err := st.Get(name); err == nil && key != "" {
			httpReq.Header.Set("x-api-key", key)
		}
	}

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return &proto.ListCloudProfileModelsResponse{Error: err.Error()}, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &proto.ListCloudProfileModelsResponse{Error: err.Error()}, nil
	}
	if resp.StatusCode >= 400 {
		return &proto.ListCloudProfileModelsResponse{
			Error: fmt.Sprintf("%s returned %d", url, resp.StatusCode),
		}, nil
	}

	// Anthropic /v1/models returns { "data": [ {id, display_name, ...}, ... ] }.
	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return &proto.ListCloudProfileModelsResponse{Error: "parse: " + err.Error()}, nil
	}
	out := &proto.ListCloudProfileModelsResponse{}
	for _, m := range parsed.Data {
		display := m.DisplayName
		if display == "" {
			display = m.ID
		}
		out.Models = append(out.Models, &proto.CloudModelInfo{
			Id: m.ID, DisplayName: display,
		})
	}
	return out, nil
}

// newHexID returns a lowercase-hex random id of nBytes*2 characters. Used for
// the x-opencode-session / x-opencode-request headers Meridian expects.
func newHexID(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "0"
	}
	return hex.EncodeToString(b)
}
