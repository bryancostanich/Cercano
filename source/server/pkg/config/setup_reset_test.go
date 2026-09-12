package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

var resetTestKeys = []string{"ollama_url", "open_runtime", "open_model", "embedding_model", "cloud_provider", "cloud_model", "cloud_api_key", "cloud_base_url", "cloud_profiles", "active_cloud_profile", "backup_cloud_profile", "secondary_cloud_profile", "secondary_backup_cloud_profile", "task_assignments", "secondary_redirect", "local_redirect", "locus_mode", "llama_server", "mistralrs", "models", "model_profiles"}

func resetTestMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestResetSetupYAMLBoundary(t *testing.T) {
	input := []byte(`ollama_url: http://old
open_runtime: ollama
open_model: old
embedding_model: old
cloud_provider: anthropic
cloud_model: old
cloud_api_key: retired-secret
cloud_base_url: http://old
cloud_profiles: [{name: old, model: old, tier_overrides: {economy: old}, image_model: old}]
active_cloud_profile: old
backup_cloud_profile: old
secondary_cloud_profile: old
secondary_backup_cloud_profile: old
task_assignments: {chat: {model: old}}
secondary_redirect: local
local_redirect: secondary
locus_mode: open_only
llama_server: {model_dirs: [/custom], default_model: old, threads: 9}
mistralrs: {model_dirs: [/custom], default_model: old, max_seqs: 9}
models: {open: {overrides: {llama_server: {everyday: old, embedding: old, vision: old}}}}
model_profiles: {cloud: {providers: {custom: {economy: {model: old}}}}}
port: "12345"
execution_mode: custom
worker_idle_timeout_seconds: -1
agent: {shutdown_on_last_client: false, future: keep}
tool_loop: {max_iterations: 17}
permissions: {custom: true}
unknown: {deep: [one, two]}
compaction: {enabled: false, summarizer_model: old, retention: {keep_forever: true}, future: keep}
watchdog: {enabled: true, model: old, mode: custom, audit: {path: /keep}, future: keep}
`)
	before := append([]byte(nil), input...)
	out, err := ResetSetupYAML(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input, before) {
		t.Fatal("mutated input")
	}
	got, old := resetTestMap(t, out), resetTestMap(t, input)
	db, err := yaml.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	defaults := resetTestMap(t, db)
	for _, k := range resetTestKeys {
		if !reflect.DeepEqual(got[k], defaults[k]) {
			t.Errorf("%s: got %#v want %#v", k, got[k], defaults[k])
		}
		delete(got, k)
		delete(old, k)
	}
	for parent, key := range map[string]string{"compaction": "summarizer_model", "watchdog": "model"} {
		g, o, d := got[parent].(map[string]any), old[parent].(map[string]any), defaults[parent].(map[string]any)
		if !reflect.DeepEqual(g[key], d[key]) {
			t.Errorf("%s.%s not default", parent, key)
		}
		delete(g, key)
		delete(o, key)
	}
	if !reflect.DeepEqual(got, old) {
		t.Errorf("preserved fields changed: %#v vs %#v", got, old)
	}
	again, err := ResetSetupYAML(out)
	if err != nil || !bytes.Equal(out, again) {
		t.Fatalf("not idempotent: %v", err)
	}
	out[0] ^= 1
	if !bytes.Equal(input, before) {
		t.Fatal("output aliases input")
	}
}

func TestResetSetupYAMLSparse(t *testing.T) {
	for _, s := range []string{"", "# empty\n", "{}", "compaction: {}\nwatchdog: {}", "compaction: null\nwatchdog: null\ntask_assignments: null"} {
		t.Run(s, func(t *testing.T) {
			out, err := ResetSetupYAML([]byte(s))
			if err != nil {
				t.Fatal(err)
			}
			cfg := Defaults()
			if err = yaml.Unmarshal(out, &cfg); err != nil {
				t.Fatal(err)
			}
			// Compare serialized semantics: YAML materializes nil slices and
			// RestartConfig records whether enabled was explicitly decoded.
			got, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			want, err := yaml.Marshal(Defaults())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("sparse config not default YAML semantics")
			}
			again, err := ResetSetupYAML(out)
			if err != nil || !bytes.Equal(out, again) {
				t.Fatal("not idempotent")
			}
		})
	}
}

func TestResetSetupYAMLRejects(t *testing.T) {
	for _, s := range []string{"[", "[]", "text", "null", "{}\n---\n{}", "{}\n---", "port: a\nport: b", "compaction: {enabled: true, enabled: false}", "unknown: {x: 1, x: 2}", "compaction: []", "watchdog: old", "llama_server: []", "models: old", "task_assignments: []", "model_profiles: []", "mistralrs: old", "cloud_profiles: {}", "x: &x {model: old}\nwatchdog: *x", "x: &x {open_model: old}\n<<: *x", "open_model: &m old\nunknown: *m", "? [a,b]\n: value"} {
		t.Run(s, func(t *testing.T) {
			input := []byte(s)
			before := append([]byte(nil), input...)
			out, err := ResetSetupYAML(input)
			if err == nil || out != nil {
				t.Fatalf("accepted unsafe input: %q", s)
			}
			if !bytes.Equal(input, before) {
				t.Fatal("mutated invalid input")
			}
		})
	}
}

func TestResetSetupYAMLLoadMigrationAndNoFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "config.yaml")
	input := []byte("cloud_provider: anthropic\ncloud_model: retired\ncloud_api_key: retired-secret\nopen_model: retired\n")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := ResetSetupYAML(input)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, input) {
		t.Fatal("transform changed file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("transform created files")
	}
	if err = os.WriteFile(path, out, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CloudProvider != "" || cfg.CloudAPIKey != "" || cfg.CloudModel != "" || len(cfg.CloudProfiles) != 0 || cfg.ActiveCloudProfile != "" || cfg.OpenModel != "" {
			t.Fatal("legacy setup resurrected")
		}
		if err = Save(cfg, path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResetSetupYAMLDoesNotEchoInvalidValues(t *testing.T) {
	const secret = "private-token-fixture"
	_, err := ResetSetupYAML([]byte("llama_server:\n  threads: " + secret + "\n"))
	if err == nil {
		t.Fatal("invalid type accepted")
	}
	if bytes.Contains([]byte(err.Error()), []byte("private")) {
		t.Fatalf("error echoes configuration data: %v", err)
	}
}
