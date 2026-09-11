package toolstack

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/profilechain"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/visioninspect"
	"cercano/source/server/pkg/config"
	"context"
	"strings"
	"testing"
)

func TestImageTargetUsesConfirmedOwnBackupBeforeLocal(t *testing.T) {
	for _, preferredImage := range []string{"", "unconfirmed"} {
		t.Run("preferred="+preferredImage, func(t *testing.T) {
			c := config.Config{ActiveCloudProfile: "p", BackupCloudProfile: "b", CloudProfiles: []config.CloudProfile{{Name: "p", ImageModel: preferredImage}, {Name: "b", ImageModel: "backup-image"}}}
			preferred, backup, local := &countingProvider{name: "preferred"}, &countingProvider{name: "backup"}, &countingProvider{name: "local"}
			chain, err := profilechain.Build(c, config.DestinationPrimary, func(p config.CloudProfile) (inference.Provider, error) {
				if p.Name == "p" {
					return profilechain.GuardVision(preferred, func(string) bool { return false }), nil
				}
				return profilechain.GuardVision(backup, func(string) bool { return true }), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			target, ok := ResolveCloudVision(chain)
			if !ok || target.Model != "backup-image" {
				t.Fatalf("target=%+v available=%v", target, ok)
			}
			store, service := BuildVision(VisionDeps{CloudTarget: func() (visioninspect.Resolved, bool) { return ResolveCloudVision(chain) }, OpenProvider: func() inference.Provider { return local }, OpenVisionModel: func() (string, bool) { return "local-image", true }, Mode: func() locus.Mode { return locus.CloudPrimary }})
			image := store.Add("conv", "image/png", []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3})
			if image.Rejected || image.Attachment == nil {
				t.Fatal("fixture rejected")
			}
			for i := 0; i < 2; i++ {
				answer, err := service.Inspect(context.Background(), "conv", image.Attachment.ID, "what is shown?")
				if err != nil || !strings.Contains(answer.Source, "[b]:backup-image") {
					t.Fatalf("inspection=%+v err=%v", answer, err)
				}
			}
			if preferred.calls != 0 || backup.calls != 1 || local.calls != 0 {
				t.Fatalf("calls preferred=%d backup=%d local=%d", preferred.calls, backup.calls, local.calls)
			}
		})
	}
}
