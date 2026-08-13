package visioninspect

import (
	"context"
	"errors"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/locus"
)

// LocusInspector implements the vision-as-tool fallback policy over a locus
// mode. Local/open vision is preferred for mixed/open modes, but cloud_only is
// literal: it must not route image inspection to a local vision model.
//
// This policy is deliberately NOT locus.Mode.Main()/Coproc(): cloud_primary can
// still prefer local vision because image inspection is cheap grunt work, while
// cloud_only remains a hard boundary.
//
//	mode          local first?   cloud allowed?
//	cloud_only    no             yes
//	cloud_primary yes            yes
//	open_primary  yes            yes
//	open_only     yes            no
//
// Fallback fires both when local is unavailable (no local vision model) AND when
// a local call fails at request time — a hung or erroring local vision model
// must not strand a turn if cloud vision is permitted and configured.
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
	if l.localAllowed() && l.local != nil && l.local.Available() {
		return true
	}
	if l.cloudAllowed() && l.cloud != nil && l.cloud.Available() {
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
	if l.localAllowed() && l.local != nil && l.local.Lookup(convID, imageID) {
		return true
	}
	if l.cloudAllowed() && l.cloud != nil && l.cloud.Lookup(convID, imageID) {
		return true
	}
	return false
}

// Inspect tries local vision first, then cloud vision when the locus permits.
// Cloud is reached when local is unavailable OR a local call fails. When cloud
// is not permitted or not configured, the local outcome (including its error)
// stands.
func (l *LocusInspector) Inspect(ctx context.Context, convID, imageID, question string) (capabilities.VisionAnswer, error) {
	if l == nil {
		return capabilities.VisionAnswer{}, errNoInner
	}

	localUsable := l.localAllowed() && l.local != nil && l.local.Available()
	cloudUsable := l.cloudAllowed() && l.cloud != nil && l.cloud.Available()

	if localUsable {
		ans, err := l.local.Inspect(ctx, convID, imageID, question)
		if err == nil {
			return ans, nil
		}
		// Local failed. Fall back to cloud only if permitted+configured;
		// otherwise the local error stands.
		if !cloudUsable {
			return capabilities.VisionAnswer{}, err
		}
		return l.cloud.Inspect(ctx, convID, imageID, question)
	}

	// No local vision path. Use cloud if permitted+configured.
	if cloudUsable {
		return l.cloud.Inspect(ctx, convID, imageID, question)
	}

	return capabilities.VisionAnswer{}, errors.New("no vision model is available for the current locus")
}
