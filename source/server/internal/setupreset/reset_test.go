package setupreset

import (
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/setupstate"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type noReadStore struct {
	secrets.Store
	gets                 int
	failList, failDelete bool
}

func (s *noReadStore) Get(string) (string, error) {
	s.gets++
	return "", errors.New("secret reads forbidden")
}
func (s *noReadStore) List() ([]string, error) {
	if s.failList {
		return nil, errors.New("fixture inaccessible")
	}
	return s.Store.List()
}
func (s *noReadStore) Delete(k string) error {
	if s.failDelete {
		return errors.New("fixture delete failure")
	}
	return s.Store.Delete(k)
}
func fixture(t *testing.T) (string, *noReadStore) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("port: 54321\nopen_model: old-model\ncloud_api_key: old-inline\ncloud_profiles:\n  - name: old\n    flavor: messages\n    tier_overrides: {premium: old}\nactive_cloud_profile: old\nwatchdog: {enabled: false, model: old-watchdog, echo: true}\ncustom_preference: keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &noReadStore{Store: secrets.NewMemory()}
	s.Set("old", "key")
	s.Set("orphan", `{"refresh_token":"fixture"}`)
	return p, s
}
func TestResetPreservesFilesAndPreferences(t *testing.T) {
	p, s := fixture(t)
	root := filepath.Dir(p)
	for _, name := range []string{"history.db", "model.gguf", "runtime-binary", "download-index.json"} {
		os.WriteFile(filepath.Join(root, name), []byte("preserve-"+name), 0600)
	}
	applied := false
	r, err := Reset(context.Background(), p, s, func(c config.Config) error {
		applied = true
		if c.Port != "54321" || len(c.CloudProfiles) != 0 || c.Watchdog.Model != "" || !c.Watchdog.Echo {
			t.Fatalf("bad live config %+v", c)
		}
		return nil
	})
	if err != nil || !applied || !r.LiveApplied || !r.ConfigWritten || r.DeletedCredentials != 2 || s.gets != 0 {
		t.Fatalf("result=%+v err=%v reads=%d", r, err, s.gets)
	}
	names, _ := s.List()
	if len(names) != 0 {
		t.Fatal("credentials survive")
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "old-inline") || !strings.Contains(string(data), "custom_preference: keep") {
		t.Fatal("bad clear/preserve boundary")
	}
	for _, name := range []string{"history.db", "model.gguf", "runtime-binary", "download-index.json"} {
		data, _ := os.ReadFile(filepath.Join(root, name))
		if string(data) != "preserve-"+name {
			t.Fatalf("changed %s", name)
		}
	}
	r, err = Reset(context.Background(), p, s, nil)
	if err != nil || r.DeletedCredentials != 0 || !r.ConfigWritten {
		t.Fatalf("retry %+v %v", r, err)
	}
}
func TestResetFailuresAreReported(t *testing.T) {
	for _, kind := range []string{"list", "delete", "apply", "publish", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			p, s := fixture(t)
			original, _ := os.ReadFile(p)
			var apply func(config.Config) error
			switch kind {
			case "list":
				s.failList = true
			case "delete":
				s.failDelete = true
			case "apply":
				apply = func(config.Config) error { return errors.New("fixture apply failure") }
			case "publish":
				apply = func(config.Config) error { os.Remove(p); return os.Mkdir(p, 0700) }
			case "malformed":
				os.WriteFile(p, []byte("open_model: ["), 0600)
			}
			r, err := Reset(context.Background(), p, s, apply)
			if err == nil || r.ConfigWritten {
				t.Fatalf("failure reported success %+v %v", r, err)
			}
			if kind == "publish" && (!r.LiveApplied || r.DeletedCredentials != 2) {
				t.Fatalf("lost partial progress %+v", r)
			}
			if kind == "list" || kind == "delete" {
				data, _ := os.ReadFile(p)
				if string(data) != string(original) {
					t.Fatal("unexpected config write")
				}
			}
			if s.gets != 0 {
				t.Fatal("read secrets")
			}
		})
	}
}
func TestMissingStateAndFreshWizard(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "new", "config.yaml")
	r, err := Reset(context.Background(), p, secrets.NewMemory(), nil)
	if err != nil || !r.ConfigWritten {
		t.Fatalf("%+v %v", r, err)
	}
	path := filepath.Join(root, "wizard.yaml")
	t.Setenv("CERCANO_WIZARD_STATE", path)
	setupstate.Save(setupstate.State{Step: setupstate.StepDone, Baseline: &setupstate.Baseline{ActiveProfile: "old"}})
	if err := WriteFreshWizard(path); err != nil {
		t.Fatal(err)
	}
	state, ok := setupstate.Load()
	if !ok || state.Step != setupstate.StepLocus || state.Baseline != nil {
		t.Fatal("stale wizard")
	}
}
func TestResetRefusesSymlinkTarget(t *testing.T) {
	p, s := fixture(t)
	link := filepath.Join(filepath.Dir(p), "link.yaml")
	os.Symlink(p, link)
	if _, err := Reset(context.Background(), link, s, nil); err == nil {
		t.Fatal("symlink accepted")
	}
	keys, _ := s.List()
	if len(keys) != 2 {
		t.Fatal("deleted credentials before validating config")
	}
	if err := WriteFreshWizard(link); err == nil {
		t.Fatal("wizard followed symlink")
	}
}
func TestCancelledResetDoesNotMutate(t *testing.T) {
	p, s := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Reset(ctx, p, s, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	keys, _ := s.List()
	if len(keys) != 2 {
		t.Fatal("cancelled reset deleted credentials")
	}
}
