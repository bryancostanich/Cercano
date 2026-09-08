package worker

import (
	"cercano/source/server/internal/modelmetadata"
	proto "cercano/source/server/pkg/proto"
)

// MarshalModelMetadata encodes host-resolved evidence without credentials.
func MarshalModelMetadata(snapshot modelmetadata.Snapshot) []*proto.ModelMetadataEntry {
	entries := make([]*proto.ModelMetadataEntry, 0, len(snapshot))
	for _, entry := range snapshot {
		evidence := entry.Evidence.Normalized()
		vision := proto.ModelMetadataEntry_UNKNOWN
		switch evidence.Vision {
		case modelmetadata.VisionSupported:
			vision = proto.ModelMetadataEntry_SUPPORTED
		case modelmetadata.VisionUnsupported:
			vision = proto.ModelMetadataEntry_UNSUPPORTED
		}
		entries = append(entries, &proto.ModelMetadataEntry{Provider: entry.Identity.Provider, BaseUrl: entry.Identity.BaseURL, Route: entry.Identity.Route, Model: entry.Identity.Model, ContextWindow: int64(evidence.ContextWindow), Vision: vision})
	}
	return entries
}

// UnmarshalModelMetadata preserves explicit unknown capability across versions.
func UnmarshalModelMetadata(entries []*proto.ModelMetadataEntry) modelmetadata.Snapshot {
	snapshot := make(modelmetadata.Snapshot, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		vision := modelmetadata.VisionUnknown
		switch entry.Vision {
		case proto.ModelMetadataEntry_SUPPORTED:
			vision = modelmetadata.VisionSupported
		case proto.ModelMetadataEntry_UNSUPPORTED:
			vision = modelmetadata.VisionUnsupported
		}
		capacity := 0
		if entry.ContextWindow > 0 && uint64(entry.ContextWindow) <= uint64(^uint(0)>>1) {
			capacity = int(entry.ContextWindow)
		}
		snapshot = append(snapshot, modelmetadata.Entry{
			Identity: modelmetadata.Identity{Provider: entry.Provider, BaseURL: entry.BaseUrl, Route: entry.Route, Model: entry.Model},
			Evidence: modelmetadata.Evidence{ContextWindow: capacity, Vision: vision},
		})
	}
	return snapshot
}
