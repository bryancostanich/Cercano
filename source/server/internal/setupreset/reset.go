// Package setupreset implements a developer-triggered, best-effort setup reset.
// It intentionally neither coordinates nor stops sessions. Callers warn that
// concurrent work may fail or write old settings/credentials back afterward.
package setupreset

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/setupstate"
	"github.com/99designs/keyring"
	"gopkg.in/yaml.v3"
)

type Result struct {
	DeletedCredentials int
	LiveApplied        bool
	ConfigWritten      bool
	Config             config.Config
}
type Prepared struct {
	YAML   []byte
	Config config.Config
}

// Prepare reads only the requested configuration; it has no write/keychain or
// runtime side effects. Its returned document no longer contains legacy keys.
func Prepare(path string) (Prepared, error) {
	if err := regularOrMissing(path); err != nil {
		return Prepared{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Prepared{}, fmt.Errorf("read setup configuration: %w", err)
	}
	output, err := config.ResetSetupYAML(data)
	if err != nil {
		return Prepared{}, err
	}
	c := config.Defaults()
	if err := yaml.Unmarshal(output, &c); err != nil {
		return Prepared{}, fmt.Errorf("reset configuration has invalid types")
	}
	if err := c.LlamaServer.Validate(); err != nil {
		return Prepared{}, fmt.Errorf("reset runtime defaults are invalid")
	}
	return Prepared{YAML: output, Config: c}, nil
}

func Reset(ctx context.Context, path string, store secrets.Store, applyLive func(config.Config) error) (Result, error) {
	var result Result
	if err := ctx.Err(); err != nil {
		return result, err
	}
	prepared, err := Prepare(path)
	if err != nil {
		return result, err
	}
	if store == nil {
		return result, fmt.Errorf("Cercano credential store is unavailable")
	}
	names, err := store.List()
	if err != nil {
		return result, fmt.Errorf("list Cercano credentials: %w", err)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		err := store.Delete(name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, keyring.ErrKeyNotFound) {
			return result, fmt.Errorf("delete Cercano credential after %d removals: %w", result.DeletedCredentials, err)
		}
		result.DeletedCredentials++
	}
	result.Config = prepared.Config
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if applyLive != nil {
		if err := applyLive(prepared.Config); err != nil {
			return result, fmt.Errorf("apply live reset configuration: %w", err)
		}
		result.LiveApplied = true
	}
	// Publish after the ordinary live state update so a file watcher does not
	// intentionally replay its old snapshot. Other sessions may still race us;
	// this debug operation makes no stronger guarantee.
	if err := writeAtomic(path, prepared.YAML); err != nil {
		return result, fmt.Errorf("write reset configuration: %w", err)
	}
	result.ConfigWritten = true
	return result, nil
}

func WriteFreshWizard(path string) error {
	data, err := yaml.Marshal(setupstate.Fresh())
	if err != nil {
		return fmt.Errorf("encode fresh setup state: %w", err)
	}
	return writeAtomic(path, data)
}
func regularOrMissing(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("setup state requires an absolute file path")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("setup target must be a regular file, not a symlink or directory")
	}
	return nil
}
func writeAtomic(path string, data []byte) error {
	if err := regularOrMissing(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".setup-reset-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err := regularOrMissing(path); err != nil {
		return err
	}
	return os.Rename(name, path)
}
