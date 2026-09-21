package loopcompact

import (
	"testing"

	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

// The DeferralError gate: a size refusal from the local summarizer should only
// reach the cloud when the user's locus actually puts co-processor work there.
// This gate lives here now — at the ONE shared summarizer construction the
// host and the worker both use — so the policy cannot drift between them.
func TestCloudIsPrimaryLocus(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		// cloud_only has nowhere else to go — deferral must be allowed to
		// spend cloud tokens or compaction cannot make progress at all.
		{string(locus.CloudOnly), true},
		// cloud_primary keeps grunt work local via Coproc(), so an oversized
		// segment defers rather than silently billing the cloud.
		{string(locus.CloudPrimary), false},
		{string(locus.OpenPrimary), false},
		{string(locus.OpenOnly), false},
		// Empty resolves to DefaultMode (cloud_primary) → local coproc.
		{"", false},
		// Garbage must not fail open into cloud spend.
		{"not_a_mode", false},
		// Legacy aliases are normalized at load time, not here; verify the
		// raw legacy string still does not fail open.
		{"local_only", false},
	} {
		if got := cloudIsPrimaryLocus(config.Config{LocusMode: tc.mode}); got != tc.want {
			t.Errorf("cloudIsPrimaryLocus(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}
