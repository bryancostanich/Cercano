package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"cercano/source/server/internal/gguf"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/sysram"
	"cercano/source/server/pkg/proto"
)

// localHeaderWindowBytes mirrors the catalog resolver's window: the
// architecture keys sit well inside the first 256 KiB of every GGUF
// we've probed, so a bounded read keeps this O(1) regardless of model
// size.
const localHeaderWindowBytes = 256 * 1024

// GetModelRAMEstimate implements proto.AgentServer. Soft-fails: all
// error paths return a response with Error set (plus SystemRamBytes,
// which is useful regardless) so clients degrade to a size-only
// display instead of surfacing an RPC failure.
func (s *Server) GetModelRAMEstimate(ctx context.Context, req *proto.GetModelRAMEstimateRequest) (*proto.GetModelRAMEstimateResponse, error) {
	resp := &proto.GetModelRAMEstimateResponse{SystemRamBytes: sysram.Total()}

	switch {
	case strings.TrimSpace(req.GetOllamaRef()) != "":
		var cm *ollamacatalog.Manager
		if s.providerSvc != nil {
			cm = s.providerSvc.CatalogManager()
		}
		if cm == nil {
			resp.Error = "online catalog not configured"
			return resp, nil
		}
		ref := normalizeOllamaRef(req.GetOllamaRef())
		est, err := cm.ResolveEstimate(ctx, ref)
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		resp.WeightsBytes = est.WeightsBytes
		resp.KvBytesPerToken = est.KVBytesPerToken
		resp.MaxContextTokens = est.MaxContextTokens
		resp.Architecture = est.Architecture
		return resp, nil

	case strings.TrimSpace(req.GetModelId()) != "":
		rm := s.runtimeMgr()
		if rm == nil {
			resp.Error = "runtime manager not configured"
			return resp, nil
		}
		models, err := rm.Inventory(ctx)
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		for _, m := range models {
			if m.ID != req.GetModelId() {
				continue
			}
			if req.GetRuntime() != "" && m.Runtime != req.GetRuntime() {
				continue
			}
			if m.Path == "" {
				resp.Error = fmt.Sprintf("model %s has no local file", m.ID)
				return resp, nil
			}
			meta, err := parseLocalGGUFHeader(m.Path)
			if err != nil {
				resp.Error = err.Error()
				return resp, nil
			}
			resp.WeightsBytes = m.SizeBytes
			resp.KvBytesPerToken = meta.KVBytesPerToken()
			resp.MaxContextTokens = int64(meta.ContextLength)
			resp.Architecture = meta.Architecture
			return resp, nil
		}
		resp.Error = fmt.Sprintf("model %s not found in inventory", req.GetModelId())
		return resp, nil

	default:
		resp.Error = "request needs ollama_ref or model_id"
		return resp, nil
	}
}

// parseLocalGGUFHeader reads the architecture keys from a GGUF on
// disk, bounded to the same window the online resolver fetches.
func parseLocalGGUFHeader(path string) (*gguf.Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return gguf.ParseMeta(io.LimitReader(f, localHeaderWindowBytes))
}
