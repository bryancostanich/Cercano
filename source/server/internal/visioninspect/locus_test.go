package visioninspect

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/locus"
)

func modeFn(m locus.Mode) func() locus.Mode { return func() locus.Mode { return m } }

// visionFrom builds a countingVision that answers with a source label so tests
// can tell which side (local/cloud) served.
func visionFrom(source string, available bool) *countingVision {
	return &countingVision{
		available: available,
		present:   map[string]bool{"c1|img_1": true},
		answer:    capabilities.VisionAnswer{Answer: "ans", Source: source},
	}
}

func TestLocus_CloudAllowed_CloudFirst(t *testing.T) {
	local := visionFrom("open:gemma", true)
	cloud := visionFrom("cloud:gpt", true)
	l := NewLocus(local, cloud, modeFn(locus.CloudPrimary))

	ans, err := l.Inspect(context.Background(), "c1", "img_1", "q?")
	if err != nil {
		t.Fatal(err)
	}
	if ans.Source != "cloud:gpt" {
		t.Fatalf("expected cloud to answer first, got source %q", ans.Source)
	}
	if local.calls != 0 {
		t.Fatalf("local must not be called when cloud succeeds, got %d", local.calls)
	}
}

func TestLocus_CloudAllowedLookupCloudFirst(t *testing.T) {
	local := visionFrom("open:gemma", true)
	cloud := visionFrom("cloud:gpt", true)
	l := NewLocus(local, cloud, modeFn(locus.OpenPrimary))

	if !l.Lookup("c1", "img_1") {
		t.Fatal("cloud-allowed lookup should find the image")
	}
	if cloud.lookups != 1 {
		t.Fatalf("cloud lookup should run first, got %d", cloud.lookups)
	}
	if local.lookups != 0 {
		t.Fatalf("local lookup should not run when cloud finds the image, got %d", local.lookups)
	}
}

func TestLocus_LocalUnavailable_CloudAllowed_CloudCalled(t *testing.T) {
	local := visionFrom("open:gemma", false) // no local vision
	cloud := visionFrom("cloud:gpt", true)
	l := NewLocus(local, cloud, modeFn(locus.CloudPrimary))

	ans, err := l.Inspect(context.Background(), "c1", "img_1", "q?")
	if err != nil {
		t.Fatal(err)
	}
	if ans.Source != "cloud:gpt" {
		t.Fatalf("expected cloud fallback, got source %q", ans.Source)
	}
	if local.calls != 0 {
		t.Fatalf("unavailable local must not be called, got %d", local.calls)
	}
	if cloud.calls != 1 {
		t.Fatalf("cloud should be called once, got %d", cloud.calls)
	}
}

func TestLocus_CloudOnlySkipsLocal(t *testing.T) {
	local := visionFrom("open:gemma", true)
	cloud := visionFrom("cloud:gpt", true)
	l := NewLocus(local, cloud, modeFn(locus.CloudOnly))

	if !l.Lookup("c1", "img_1") {
		t.Fatal("cloud_only should resolve image presence through cloud side")
	}
	if local.lookups != 0 {
		t.Fatalf("cloud_only lookup must not touch local vision, got %d", local.lookups)
	}
	if cloud.lookups != 1 {
		t.Fatalf("cloud lookup should run once, got %d", cloud.lookups)
	}

	ans, err := l.Inspect(context.Background(), "c1", "img_1", "q?")
	if err != nil {
		t.Fatal(err)
	}
	if ans.Source != "cloud:gpt" {
		t.Fatalf("cloud_only should use cloud vision, got source %q", ans.Source)
	}
	if local.calls != 0 {
		t.Fatalf("cloud_only must not call local vision, got %d", local.calls)
	}
	if cloud.calls != 1 {
		t.Fatalf("cloud should be called once, got %d", cloud.calls)
	}
}

func TestLocus_LocalUnavailable_OpenOnly_NoCloud(t *testing.T) {
	local := visionFrom("open:gemma", false)
	cloud := visionFrom("cloud:gpt", true)
	l := NewLocus(local, cloud, modeFn(locus.OpenOnly))

	_, err := l.Inspect(context.Background(), "c1", "img_1", "q?")
	if err == nil {
		t.Fatal("expected unavailable error under open_only with no local vision")
	}
	if cloud.calls != 0 {
		t.Fatalf("open_only must never call cloud, got %d", cloud.calls)
	}
}

func TestLocus_CloudFailure_FallsBackToLocal(t *testing.T) {
	local := visionFrom("open:gemma", true)
	cloud := visionFrom("cloud:gpt", true)
	cloud.err = errors.New("cloud backend down")
	l := NewLocus(local, cloud, modeFn(locus.CloudPrimary))

	ans, err := l.Inspect(context.Background(), "c1", "img_1", "q?")
	if err != nil {
		t.Fatalf("expected local to answer after cloud failure, got %v", err)
	}
	if ans.Source != "open:gemma" {
		t.Fatalf("expected local fallback, got source %q", ans.Source)
	}
	if local.calls != 1 || cloud.calls != 1 {
		t.Fatalf("expected cloud tried then local, got local=%d cloud=%d", local.calls, cloud.calls)
	}
}

func TestLocus_LocalFailure_OpenOnly_ErrorStands(t *testing.T) {
	local := visionFrom("open:gemma", true)
	local.err = errors.New("local backend down")
	cloud := visionFrom("cloud:gpt", true)
	l := NewLocus(local, cloud, modeFn(locus.OpenOnly))

	_, err := l.Inspect(context.Background(), "c1", "img_1", "q?")
	if err == nil || err.Error() != "local backend down" {
		t.Fatalf("open_only local failure must surface the local error, got %v", err)
	}
	if cloud.calls != 0 {
		t.Fatalf("open_only must not call cloud, got %d", cloud.calls)
	}
}

func TestLocus_CloudFallbackLabel(t *testing.T) {
	// A cloud fallback result must honestly carry the cloud source label so the
	// envelope reflects who answered.
	local := visionFrom("open:gemma", false)
	cloud := visionFrom("cloud:gpt-5", true)
	l := NewLocus(local, cloud, modeFn(locus.OpenPrimary))

	ans, _ := l.Inspect(context.Background(), "c1", "img_1", "q?")
	if ans.Source != "cloud:gpt-5" {
		t.Fatalf("cloud fallback envelope must name the cloud source, got %q", ans.Source)
	}
}

func TestLocus_Available(t *testing.T) {
	cases := []struct {
		name      string
		local     bool
		cloud     bool
		mode      locus.Mode
		wantAvail bool
	}{
		{"local only, open_only", true, false, locus.OpenOnly, true},
		{"local only, cloud_only", true, false, locus.CloudOnly, false},
		{"cloud only, open_only", false, true, locus.OpenOnly, false},
		{"cloud only, cloud_only", false, true, locus.CloudOnly, true},
		{"cloud only, cloud_primary", false, true, locus.CloudPrimary, true},
		{"neither, cloud_primary", false, false, locus.CloudPrimary, false},
		{"both, open_only", true, true, locus.OpenOnly, true},
		{"both, cloud_only", true, true, locus.CloudOnly, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := NewLocus(visionFrom("l", c.local), visionFrom("c", c.cloud), modeFn(c.mode))
			if got := l.Available(); got != c.wantAvail {
				t.Fatalf("Available() = %v, want %v", got, c.wantAvail)
			}
		})
	}
}

func TestLocus_NilSides(t *testing.T) {
	// No local, cloud permitted+configured.
	l := NewLocus(nil, visionFrom("cloud", true), modeFn(locus.CloudPrimary))
	if !l.Available() {
		t.Fatal("cloud-only wiring should be available under cloud_primary")
	}
	if _, err := l.Inspect(context.Background(), "c1", "img_1", "q?"); err != nil {
		t.Fatalf("cloud-only should answer, got %v", err)
	}

	// Both nil → unavailable, error.
	empty := NewLocus(nil, nil, modeFn(locus.CloudPrimary))
	if empty.Available() {
		t.Fatal("no sides wired should be unavailable")
	}
	if _, err := empty.Inspect(context.Background(), "c1", "img_1", "q?"); err == nil {
		t.Fatal("no sides wired Inspect must error")
	}
}
