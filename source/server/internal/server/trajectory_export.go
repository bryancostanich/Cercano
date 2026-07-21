package server

import (
	"cercano/source/server/internal/trajectory"
	"cercano/source/server/pkg/proto"
)

// ExportTrajectory implements proto.AgentServer. The server owns the full
// export: persistence reads, ATIF mapping, artifact collection, redaction,
// validation, and zip packaging. Clients only supply options and render the
// streamed progress events.
func (s *Server) ExportTrajectory(req *proto.ExportTrajectoryRequest, stream proto.Agent_ExportTrajectoryServer) error {
	store := s.persistSvc.Store()
	if store == nil {
		return stream.Send(&proto.ExportTrajectoryEvent{Payload: &proto.ExportTrajectoryEvent_Failed{Failed: &proto.ExportTrajectoryFailed{Code: "no_store", Message: "conversation store is not configured"}}})
	}
	exp := trajectory.Exporter{Store: store}
	version := s.buildVersion
	if version == "" {
		version = "dev"
	}
	res, err := exp.Export(stream.Context(), trajectory.Options{
		ConversationID: req.GetConversationId(),
		OutPath:        req.GetOutPath(),
		Format:         trajectory.Format(req.GetFormat()),
		Redaction:      trajectory.RedactionMode(req.GetRedactionMode()),
		IncludeLogs:    req.GetIncludeLogs(),
		Overwrite:      req.GetOverwrite(),
		Version:        version,
	}, func(phase, message string) {
		_ = stream.Send(&proto.ExportTrajectoryEvent{Payload: &proto.ExportTrajectoryEvent_Progress{Progress: &proto.ExportTrajectoryProgress{Phase: phase, Message: message}}})
	})
	if err != nil {
		return stream.Send(&proto.ExportTrajectoryEvent{Payload: &proto.ExportTrajectoryEvent_Failed{Failed: &proto.ExportTrajectoryFailed{Code: "export_failed", Message: err.Error()}}})
	}
	for _, w := range res.Warnings {
		if err := stream.Send(&proto.ExportTrajectoryEvent{Payload: &proto.ExportTrajectoryEvent_Warning{Warning: &proto.ExportTrajectoryWarning{Code: "warning", Message: w}}}); err != nil {
			return err
		}
	}
	return stream.Send(&proto.ExportTrajectoryEvent{Payload: &proto.ExportTrajectoryEvent_Completed{Completed: &proto.ExportTrajectoryCompleted{OutputPath: res.OutputPath, ManifestPath: res.ManifestPath, ArtifactCount: int32(res.ArtifactCount), SubagentCount: int32(res.SubagentCount)}}})
}
