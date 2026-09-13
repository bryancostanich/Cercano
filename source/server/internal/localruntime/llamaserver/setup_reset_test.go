package llamaserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/setupreset"
	"cercano/source/server/pkg/config"
)

func TestSetupResetRediscoversPreservedDefaultDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	catalog, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var id string
	var urls []string
	for _, model := range catalog.Models {
		if u := model.DownloadURLs(); len(u) > 0 {
			id = model.ID
			urls = u
			break
		}
	}
	if id == "" {
		t.Fatal("catalog fixture missing models")
	}
	defaultDir := filepath.Join(home, ".cercano", "models")
	modelDir := filepath.Join(defaultDir, localruntime.ModelDirName(id))
	if err := os.MkdirAll(modelDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, u := range urls {
		if err := os.WriteFile(filepath.Join(modelDir, urlFilename(u)), []byte("existing-model-bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	custom := filepath.Join(home, "custom-models")
	os.MkdirAll(custom, 0700)
	os.WriteFile(filepath.Join(custom, "keep.gguf"), []byte("custom-bytes"), 0600)
	c := config.Defaults()
	c.LlamaServer.ModelDirs = []string{custom}
	c.OpenModel = "old-choice"
	path := filepath.Join(home, "config.yaml")
	if err := config.Save(c, path); err != nil {
		t.Fatal(err)
	}
	result, err := setupreset.Reset(context.Background(), path, secrets.NewMemory(), nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := NewProvider(result.Config.LlamaServer)
	if provider.catalogTargetDir() != defaultDir {
		t.Fatalf("not reset to normalized directory: %s", provider.catalogTargetDir())
	}
	found := false
	for _, model := range provider.CatalogModels() {
		if model.ID == runtimeName+":catalog:"+id {
			found = true
			if model.DownloadState != localruntime.Downloaded {
				t.Fatalf("preserved model needs download: %+v", model)
			}
		}
	}
	if !found {
		t.Fatal("preserved download not discoverable")
	}
	for _, u := range urls {
		data, _ := os.ReadFile(filepath.Join(modelDir, urlFilename(u)))
		if string(data) != "existing-model-bytes" {
			t.Fatal("model bytes changed")
		}
	}
	data, _ := os.ReadFile(filepath.Join(custom, "keep.gguf"))
	if string(data) != "custom-bytes" {
		t.Fatal("custom model deleted")
	}
}
