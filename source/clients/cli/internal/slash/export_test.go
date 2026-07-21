package slash

import "testing"

func TestSlash_ExportTrajectory_OpensExportFlow(t *testing.T) {
	r := New()
	RegisterExport(r)
	res, _ := r.Dispatch("/export trajectory ~/Downloads/run.zip")
	if res.Kind != ResultOpenTrajectoryExport {
		t.Fatalf("kind = %v, want ResultOpenTrajectoryExport", res.Kind)
	}
	if res.Text != "~/Downloads/run.zip" {
		t.Fatalf("text = %q", res.Text)
	}
}

func TestSlash_TrajAlias_OpensExportFlow(t *testing.T) {
	r := New()
	RegisterExport(r)
	res, _ := r.Dispatch("/traj")
	if res.Kind != ResultOpenTrajectoryExport {
		t.Fatalf("kind = %v, want ResultOpenTrajectoryExport", res.Kind)
	}
}
