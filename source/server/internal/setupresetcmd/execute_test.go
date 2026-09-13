package setupresetcmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/setupstate"
)

func TestLocalResetAndFreshWizard(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	wizardPath := filepath.Join(root, "wizard.yaml")
	os.WriteFile(configPath, []byte("port: '54321'\nopen_model: old\ncustom_preference: keep\n"), 0600)
	store := secrets.NewMemory()
	store.Set("orphan", "fixture")
	outcome, err := Perform(context.Background(), Dependencies{ConfigPath: configPath, WizardPath: wizardPath, OpenStore: func() (secrets.Store, error) { return store, nil }, TryAgent: func(_ context.Context, addr string) (Outcome, error) {
		if addr != "127.0.0.1:54321" {
			t.Fatal(addr)
		}
		return Outcome{}, ErrNoAgent
	}})
	if err != nil || !outcome.ConfigWritten || !outcome.WizardWritten || outcome.UsedAgent || outcome.CredentialsRemoved != 1 {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	t.Setenv("CERCANO_WIZARD_STATE", wizardPath)
	s, ok := setupstate.Load()
	if !ok || s.Step != setupstate.StepLocus || s.Baseline != nil {
		t.Fatal("not fresh setup")
	}
}
func TestReachableAgentNeverFallsBackToLocalStore(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			wizardPath := filepath.Join(root, "wizard.yaml")
			outcome, err := Perform(context.Background(), Dependencies{ConfigPath: path, WizardPath: wizardPath, OpenStore: func() (secrets.Store, error) { t.Fatal("local keychain touched with reachable agent"); return nil, nil }, TryAgent: func(context.Context, string) (Outcome, error) {
				o := Outcome{UsedAgent: true, ConfigWritten: !failed, LiveApplied: !failed, CredentialsRemoved: 3}
				if failed {
					return o, errors.New("fixture partial error")
				}
				return o, nil
			}})
			if failed {
				if err == nil || outcome.WizardWritten || outcome.CredentialsRemoved != 3 {
					t.Fatalf("lost partial result %+v %v", outcome, err)
				}
				if _, err := os.Stat(wizardPath); !os.IsNotExist(err) {
					t.Fatal("wizard changed after failed reset")
				}
			} else if err != nil || !outcome.UsedAgent || !outcome.WizardWritten {
				t.Fatalf("result %+v %v", outcome, err)
			}
		})
	}
}
func TestWizardFailureReportsCompletedSettingsReset(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	outcome, err := Perform(context.Background(), Dependencies{ConfigPath: path, WizardPath: root, OpenStore: func() (secrets.Store, error) { return secrets.NewMemory(), nil }})
	if err == nil || !outcome.ConfigWritten || outcome.WizardWritten {
		t.Fatalf("partial result %+v %v", outcome, err)
	}
}

func TestWizardPathCannotOverwriteConfig(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "config.yaml")
	os.WriteFile(p, []byte("port: '54321'\n"), 0600)
	called := false
	_, err := Perform(context.Background(), Dependencies{ConfigPath: p, WizardPath: p, OpenStore: func() (secrets.Store, error) { called = true; return secrets.NewMemory(), nil }})
	if err == nil || called {
		t.Fatalf("config/wizard collision not rejected before reset: called=%t err=%v", called, err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "port: '54321'\n" {
		t.Fatal("config overwritten by wizard")
	}
}
