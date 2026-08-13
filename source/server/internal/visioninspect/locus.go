package visioninspect

import (
	"context"
	"errors"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/locus"
)

// LocusInspector implements the vision-as-tool fallback policy over a locus
// mode. Cloud vision is preferred whenever the current locus permits cloud,
// because the local vision model lane is optional and may be unavailable or
// unvalidated. open_only remains a hard no-cloud boundary.
//
// This policy is deliberately NOT locus.Mode.Main()/Coproc(): image inspection
// is a leaf tool call, not the main reasoning lane.
//
//	mode          cloud first?   local allowed?
//	cloud_only    yes            no
//	cloud_primary yes            yes
//	open_primary  yes            yes
//	open_only     no             yes
//
// Fallback fires both when the preferred side is unavailable and when its call
// fails at request time, as long as the fallback side is permitted/configured.
type LocusInspector struct {
	local capabilities.VisionService // open/local vision (may be nil)
	cloud capabilities.VisionService // cloud vision (may be nil)
	mode  func() locus.Mode          // read live so a mode change takes effect per call
}

var _ capabilities.VisionService = (*LocusInspector)(nil)

// NewLocus builds a locus-aware vision service. local and cloud may each be nil
// (e.g. no cloud provider configured). mode is read on every call so a runtime
// locus change is honored without rebuilding.
func NewLocus(local, cloud capabilities.VisionService, mode func() locus.Mode) *LocusInspector {
	return &LocusInspector{local: local, cloud: cloud, mode: mode}
}

// cloudAllowed reports whether the current locus permits cloud vision. Cloud is
// allowed for every mode except open_only.
func (l *LocusInspector) cloudAllowed() bool {
	if l.mode == nil {
		return false
	}
	return l.mode() != locus.OpenOnly
}

// localAllowed reports whether the current locus permits local/open vision.
// cloud_only is a hard boundary: it must not route image inspection locally.
func (l *LocusInspector) localAllowed() bool {
	if l.mode == nil {
		return true
	}
	return l.mode() != locus.CloudOnly
}

// Available reports whether ANY permitted vision path is available: local, or
// (when the locus allows it) cloud.
func (l *LocusInspector) Available() bool {
	if l == nil {
		return false
	}
	if l.cloudAllowed() && l.cloud != nil && l.cloud.Available() {
		return true
	}
	if l.localAllowed() && l.local != nil && l.local.Available() {
		return true
	}
	return false
}

// Lookup reports presence via whichever service backs the current path. The
// attachment store is shared per-conversation, so local and cloud Lookup agree;
// checking local first (then cloud when permitted) covers deployments where only
// one side is wired.
func (l *LocusInspector) Lookup(convID, imageID string) bool {
	if l == nil {
		return false
	}
	if l.cloudAllowed() && l.cloud != nil && l.cloud.Lookup(convID, imageID) {
		return true
	}
	if l.localAllowed() && l.local != nil && l.local.Lookup(convID, imageID) {
		return true
	}
	return false
}

// Inspect tries cloud vision first whenever cloud is permitted, then falls back
// to local/open vision when cloud is unavailable or fails. Under open_only,
// cloud is never called and the local outcome stands.
func (l *LocusInspector) Inspect(ctx context.Context, convID, imageID, question string) (capabilities.VisionAnswer, error) {
	if l == nil {
		return capabilities.VisionAnswer{}, errNoInner
	}

	localUsable := l.localAllowed() && l.local != nil && l.local.Available()
	cloudUsable := l.cloudAllowed() && l.cloud != nil && l.cloud.Available()

	if cloudUsable {
		ans, err := l.cloud.Inspect(ctx, convID, imageID, question)
		if err == nil {
			return ans, nil
		}
		if !localUsable {
			return capabilities.VisionAnswer{}, err
		}
		return l.local.Inspect(ctx, convID, imageID, question)
	}

	if localUsable {
		return l.local.Inspect(ctx, convID, imageID, question)
	}

	return capabilities.VisionAnswer{}, errors.New("no vision model is available for the current locus")
}
