package toolstack

import (
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/visionattach"
	"cercano/source/server/internal/visioninspect"
)

// VisionDeps are the narrow seams BuildVision needs to assemble the live
// vision-as-tool service, expressed as functions so this package stays free of
// openmodels / hostsvc-providers imports (and any import-cycle risk). Every
// function is read live at call time, so a runtime config or provider swap is
// honored without rebuilding the inspector.
type VisionDeps struct {
	// OpenProvider yields the local/open inference provider that serves the
	// vision GGUF (the llama-server engine warms the vision model — with its
	// mmproj — on demand from the model id). May return nil (no open provider).
	OpenProvider func() inference.Provider
	// OpenVisionModel yields the effective open vision-tier model id and ok=false
	// when no vision model is configured (the normal "vision unavailable" state).
	OpenVisionModel func() (string, bool)
	// CloudProvider yields the cloud inference provider used for vision fallback,
	// or nil when cloud vision is not wired. May be nil.
	CloudProvider func() inference.Provider
	// CloudVisionModel yields the cloud vision model id and ok=false when cloud
	// vision is not configured. May be nil (equivalent to always-false).
	CloudVisionModel func() (string, bool)
	// Mode yields the current locus mode, read live per call so a runtime mode
	// change takes effect. Governs whether cloud fallback is permitted at all
	// (every mode except open_only).
	Mode func() locus.Mode
}

// BuildVision assembles the shared per-conversation attachment store and the
// live vision service that backs inspect_image, and returns BOTH: the store
// must be handed to the tool-loop path (runner.Deps.VisionStore) so the leading
// user turn's images are registered, and the service must be handed to
// InstallCapabilities (CapDeps.Vision) so inspect_image can look them up. The
// two share the one store, so a rewrite and a later lookup agree.
//
// The service is a caching inspector over a locus-aware inspector: local/open
// vision is tried first everywhere (a vision question is cheap grunt work that
// stays local even under cloud_primary), with cloud fallback only when the
// locus permits it. Each side is a plain visioninspect.Inspector over the shared
// store and a resolver derived from the supplied seams; a nil provider or a
// model-resolution miss makes that side report unavailable, so a partially-wired
// deployment degrades to a clear "vision unavailable" rather than an error.
//
// The host and worker call this identically so their turn-execution environments
// never diverge.
func BuildVision(d VisionDeps) (*visionattach.Store, capabilities.VisionService) {
	store := visionattach.NewStore()

	localResolver := func() (visioninspect.Resolved, bool) {
		if d.OpenProvider == nil || d.OpenVisionModel == nil {
			return visioninspect.Resolved{}, false
		}
		id, ok := d.OpenVisionModel()
		if !ok || id == "" {
			return visioninspect.Resolved{}, false
		}
		prov := d.OpenProvider()
		if prov == nil {
			return visioninspect.Resolved{}, false
		}
		return visioninspect.Resolved{Provider: prov, Model: id}, true
	}
	local := visioninspect.New(store, localResolver)

	var cloud capabilities.VisionService
	if d.CloudProvider != nil && d.CloudVisionModel != nil {
		cloudResolver := func() (visioninspect.Resolved, bool) {
			id, ok := d.CloudVisionModel()
			if !ok || id == "" {
				return visioninspect.Resolved{}, false
			}
			prov := d.CloudProvider()
			if prov == nil {
				return visioninspect.Resolved{}, false
			}
			return visioninspect.Resolved{Provider: prov, Model: id}, true
		}
		cloud = visioninspect.New(store, cloudResolver)
	}

	mode := d.Mode
	if mode == nil {
		mode = func() locus.Mode { return locus.DefaultMode }
	}
	svc := visioninspect.NewCaching(visioninspect.NewLocus(local, cloud, mode))
	return store, svc
}
