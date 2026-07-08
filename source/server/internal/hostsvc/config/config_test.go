package config_test

import (
	"sync"
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	cfg "cercano/source/server/pkg/config"
)

// TestServiceRaceGet races concurrent Get()+scalar-read against Set() to
// exercise the basic struct-copy path under the race detector.
func TestServiceRaceGet(t *testing.T) {
	svc := cfgsvc.New("", cfg.Config{LocusMode: "cloud_primary"}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c := svc.Get()
				_ = c.LocusMode
				svc.Set(cfg.Config{LocusMode: "open_primary"})
			}
		}()
	}
	wg.Wait()
}

// TestServiceRaceCloudProfiles is the characterization test for the snapshot
// data race: concurrent Get()+full-slice-iteration must not race against
// UpsertProfile/RemoveProfile that mutate the same backing array.
//
// Without Clone() in Get(), -race detects: concurrent write in UpsertProfile
// (s.current.CloudProfiles[i] = p) vs read in the caller iterating the
// returned snapshot slice (same backing array).
//
// With Clone() this test must pass -race cleanly.
func TestServiceRaceCloudProfiles(t *testing.T) {
	profiles := make([]cfg.CloudProfile, 5)
	for i := range profiles {
		profiles[i] = cfg.CloudProfile{Name: string(rune('a' + i)), Model: "m"}
	}
	svc := cfgsvc.New("", cfg.Config{
		CloudProfiles:      profiles,
		ActiveCloudProfile: "a",
	}, nil)

	var wg sync.WaitGroup

	// Writers: interleave upserts and removes.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			name := string(rune('a' + idx%5))
			for j := 0; j < 200; j++ {
				if j%2 == 0 {
					svc.UpsertProfile(cfg.CloudProfile{Name: name, Model: "updated"})
				} else {
					svc.RemoveProfile(name)
					svc.UpsertProfile(cfg.CloudProfile{Name: name, Model: "restored"})
				}
			}
		}()
	}

	// Readers: get snapshot and iterate the entire CloudProfiles slice.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				snap := svc.Get()
				// Iterate every element — this is what would race against
				// UpsertProfile's in-place write if the snapshot shared the
				// backing array.
				for k := range snap.CloudProfiles {
					_ = snap.CloudProfiles[k].Name
					_ = snap.CloudProfiles[k].Model
				}
				// Also iterate Watchdog.Checks and LlamaServer.ModelDirs to
				// cover the other cloned slices.
				for k := range snap.Watchdog.Checks {
					_ = snap.Watchdog.Checks[k]
				}
				for k := range snap.LlamaServer.ModelDirs {
					_ = snap.LlamaServer.ModelDirs[k]
				}
			}
		}()
	}

	wg.Wait()
}

func TestServiceSetActiveProfile(t *testing.T) {
	svc := cfgsvc.New("", cfg.Config{
		CloudProfiles: []cfg.CloudProfile{
			{Name: "a", Model: "m1"},
			{Name: "b", Model: "m2"},
		},
	}, nil)

	if svc.SetActiveProfile("nope") {
		t.Error("SetActiveProfile should return false for unknown profile")
	}
	if !svc.SetActiveProfile("a") {
		t.Error("SetActiveProfile should return true for known profile")
	}
	c := svc.Get()
	if c.ActiveCloudProfile != "a" {
		t.Errorf("got ActiveCloudProfile=%q, want %q", c.ActiveCloudProfile, "a")
	}
}

func TestServiceUpsertProfile(t *testing.T) {
	svc := cfgsvc.New("", cfg.Config{
		CloudProfiles:      []cfg.CloudProfile{{Name: "a", Model: "m1"}},
		ActiveCloudProfile: "a",
	}, nil)

	replaced, isActive := svc.UpsertProfile(cfg.CloudProfile{Name: "a", Model: "m2"})
	if !replaced {
		t.Error("UpsertProfile should report replaced=true for existing name")
	}
	if !isActive {
		t.Error("UpsertProfile should report isActive=true for active profile")
	}
	c := svc.Get()
	if c.CloudProfiles[0].Model != "m2" {
		t.Errorf("got model %q, want %q", c.CloudProfiles[0].Model, "m2")
	}

	replaced, _ = svc.UpsertProfile(cfg.CloudProfile{Name: "new", Model: "m3"})
	if replaced {
		t.Error("UpsertProfile should report replaced=false for new name")
	}
	c = svc.Get()
	if len(c.CloudProfiles) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(c.CloudProfiles))
	}
}

func TestServiceRemoveProfile(t *testing.T) {
	svc := cfgsvc.New("", cfg.Config{
		CloudProfiles:      []cfg.CloudProfile{{Name: "a", Model: "m1"}, {Name: "b", Model: "m2"}},
		ActiveCloudProfile: "a",
	}, nil)

	existed, wasActive := svc.RemoveProfile("a")
	if !existed {
		t.Error("RemoveProfile should report existed=true")
	}
	if !wasActive {
		t.Error("RemoveProfile should report wasActive=true")
	}
	c := svc.Get()
	if c.ActiveCloudProfile != "" {
		t.Errorf("expected empty ActiveCloudProfile after removing active, got %q", c.ActiveCloudProfile)
	}
	if len(c.CloudProfiles) != 1 {
		t.Errorf("expected 1 profile after remove, got %d", len(c.CloudProfiles))
	}

	existed, wasActive = svc.RemoveProfile("nope")
	if existed || wasActive {
		t.Error("RemoveProfile of unknown name should return false, false")
	}
}

func TestServiceActiveProfile(t *testing.T) {
	svc := cfgsvc.New("", cfg.Config{
		CloudProfiles:      []cfg.CloudProfile{{Name: "a", Model: "m1"}},
		ActiveCloudProfile: "a",
	}, nil)

	p, ok := svc.ActiveProfile()
	if !ok {
		t.Error("ActiveProfile should return ok=true")
	}
	if p.Name != "a" || p.Model != "m1" {
		t.Errorf("unexpected profile %+v", p)
	}

	svc2 := cfgsvc.New("", cfg.Config{}, nil)
	_, ok = svc2.ActiveProfile()
	if ok {
		t.Error("ActiveProfile on empty config should return ok=false")
	}
}

func TestServiceMutate(t *testing.T) {
	svc := cfgsvc.New("", cfg.Config{LocusMode: "cloud_only"}, nil)
	svc.Mutate(func(c *cfg.Config) {
		c.LocusMode = "open_only"
	})
	c := svc.Get()
	if c.LocusMode != "open_only" {
		t.Errorf("Mutate did not update LocusMode: got %q", c.LocusMode)
	}
}

func TestServiceSetCloudModel(t *testing.T) {
	svc := cfgsvc.New("", cfg.Config{CloudModel: "old"}, nil)
	svc.SetCloudModel("new")
	c := svc.Get()
	if c.CloudModel != "new" {
		t.Errorf("SetCloudModel: got %q, want %q", c.CloudModel, "new")
	}
}

func TestServiceSetBackupProfile(t *testing.T) {
	svc := cfgsvc.New("", cfg.Config{
		CloudProfiles: []cfg.CloudProfile{{Name: "a"}, {Name: "b"}},
	}, nil)
	if !svc.SetBackupProfile("b") {
		t.Error("SetBackupProfile should return true for existing profile")
	}
	c := svc.Get()
	if c.BackupCloudProfile != "b" {
		t.Errorf("got BackupCloudProfile=%q, want %q", c.BackupCloudProfile, "b")
	}
	if svc.SetBackupProfile("nope") {
		t.Error("SetBackupProfile should return false for unknown profile")
	}
	// Clearing (empty string) always ok.
	if !svc.SetBackupProfile("") {
		t.Error("SetBackupProfile with empty string should return true")
	}
}
