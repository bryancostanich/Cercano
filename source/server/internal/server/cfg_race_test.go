package server

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"cercano/source/server/pkg/proto"
)

// TestConfigConcurrentAccessRace runs profile mutations concurrently with the
// config-read handlers and the UpdateConfig apply path (the watcher's path).
// Run under -race; it must not race or deadlock.
//
// Calls exercised, and why:
//   - UpsertCloudProfile / RemoveCloudProfile: in-place CloudProfiles slice
//     mutation (append / [i]= / [:0] filter) — the widened race this task fixes.
//   - GetCloudProfiles / GetConfig: concurrent reads of currentConfig (the
//     ActiveCloudProfile + profile metadata snapshot, and the field snapshot).
//   - UpdateConfig{LocusMode}: the watcher's apply path. It takes the write lock
//     across the whole body, so this directly exercises the watcher-vs-handler
//     race (writer holding cfgMu while readers/profile-writers contend). Safe in
//     the bare newTestServer: registry/coordinator are nil-guarded, no cloud
//     fields means cloudFactory is never invoked, events is nil so no broadcast,
//     and configPath is "" so config.Save is skipped.
func TestConfigConcurrentAccessRace(t *testing.T) {
	s, _ := newTestServer() // has currentConfig + memory secrets
	ctx := context.Background()
	var wg sync.WaitGroup

	// Profile-mutation + read workers.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				name := fmt.Sprintf("p-%d-%d", g, i)
				s.UpsertCloudProfile(ctx, &proto.UpsertCloudProfileRequest{Name: name, Flavor: "messages"})
				s.GetCloudProfiles(ctx, &proto.GetCloudProfilesRequest{})
				s.GetConfig(ctx, &proto.GetConfigRequest{})
				s.RemoveCloudProfile(ctx, &proto.RemoveCloudProfileRequest{Name: name})
			}
		}(g)
	}

	// Watcher apply-path workers: drive UpdateConfig (write lock) concurrently
	// with the profile mutations above.
	modes := []string{"local_only", "cloud_only", "local_primary", "cloud_primary"}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				s.UpdateConfig(ctx, &proto.UpdateConfigRequest{LocusMode: modes[(g+i)%len(modes)]})
				s.LocusMode()
			}
		}(g)
	}

	wg.Wait()
}
