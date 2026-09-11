package toolstack

import (
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/visionattach"
	"cercano/source/server/internal/visioninspect"
	"cercano/source/server/pkg/config"
)

// VisionDeps are the narrow seams BuildVision needs to assemble the live
// vision-as-tool service, expressed as functions so this package stays free of
// openmodels / hostsvc-providers imports (and any import-cycle risk). Every
// function is read live at call time, so a runtime config or provider swap is
// honored without rebuilding the inspector.
type VisionDeps struct {
	// CloudTarget resolves one atomic, evidence-confirmed preferred/backup target.
	CloudTarget func() (visioninspect.Resolved, bool)
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
	// CloudVisionConfirmed reports whether the given cloud model has confirmed
	// image-input capability. It gates every cloud image request: a model whose
	// capability is unknown or known-absent is treated as no cloud vision lane
	// at all, so the locus falls back to open/local or reports unavailable.
	//
	// This is deliberately separate from the provider's SupportsVision
	// capability flag. That flag describes the TRANSPORT — whether the client
	// can encode image blocks — and every OpenAI-compatible client sets it
	// unconditionally. Being able to send an image is not evidence that the
	// selected model can read one.
	//
	// nil means unconfirmed for every model, which closes the cloud lane. That
	// is the safe default for a partially-wired deployment: silence must not
	// authorize sending user images to a model that may not support them.
	CloudVisionConfirmed func(model string) bool
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
// The service is a caching inspector over a locus-aware inspector: cloud vision
// is preferred whenever the locus permits cloud, with local/open as fallback and
// open_only as a hard no-cloud boundary. Each side is a plain
// visioninspect.Inspector over the shared store and a resolver derived from the
// supplied seams; a nil provider or a model-resolution miss makes that side
// report unavailable, so a partially-wired deployment degrades to a clear
// "vision unavailable" rather than an error.
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
	if d.CloudTarget != nil {
		cloud = visioninspect.New(store, d.CloudTarget)
	} else if d.CloudProvider != nil && d.CloudVisionModel != nil {
		cloudResolver := func() (visioninspect.Resolved, bool) {
			id, ok := d.CloudVisionModel()
			if !ok || id == "" {
				return visioninspect.Resolved{}, false
			}
			// Confirmed-capability gate. Resolving to "no cloud target" rather
			// than erroring is what preserves the existing policy: the locus
			// wrapper then tries the open/local lane exactly as it does for an
			// unconfigured cloud profile, and reports unavailable only if that
			// lane is missing too.
			if d.CloudVisionConfirmed == nil || !d.CloudVisionConfirmed(id) {
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

// ResolveCloudVision uses the same intent-aware chain target as the request.
// It can select a configured, confirmed backup without first sending an image
// to an unavailable or unconfirmed preferred model.
func ResolveCloudVision(provider inference.Provider) (visioninspect.Resolved, bool) {
	if provider == nil {
		return visioninspect.Resolved{}, false
	}
	target := inference.TargetForCall(provider, inference.Call{Tier: string(config.TierVision)})
	if target.Model == "" || !target.VisionKnown || !target.SupportsVision {
		return visioninspect.Resolved{}, false
	}
	return visioninspect.Resolved{Provider: provider, Model: target.Model}, true
}
