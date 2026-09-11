package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextOverrideRoundTrips(t *testing.T) {
	for _, value := range []string{"", "null", "8192", "65536"} {
		t.Run("value_"+value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			body := "llama_server:\n  enabled: true\n"
			if value != "" {
				body += "  context_size: " + value + "\n"
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				c, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				automatic := value == "" || value == "null"
				if automatic && c.LlamaServer.ContextSize != nil {
					t.Fatal("automatic became explicit")
				}
				if !automatic && fmt.Sprint(c.LlamaServer.ContextOverride()) != value {
					t.Fatal("override changed")
				}
				c.Port = "50053" // unrelated settings save
				if err := Save(c, path); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if automatic && strings.Contains(string(data), "context_size:") {
					t.Fatal("automatic serialized as an override")
				}
			}
		})
	}
}

func TestContextOverrideRejectsNonpositive(t *testing.T) {
	for _, n := range []int{0, -1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(fmt.Sprintf("llama_server:\n  context_size: %d\n", n)), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load accepted nonpositive override")
			}
			c := Defaults()
			c.LlamaServer.ContextSize = &n
			if err := Save(c, path); err == nil {
				t.Fatal("Save accepted nonpositive override")
			}
		})
	}
}

func TestContextOverrideCloneIsolation(t *testing.T) {
	n := 8192
	c := Defaults()
	c.LlamaServer.ContextSize = &n
	clone := c.Clone()
	*clone.LlamaServer.ContextSize = 65536
	if *c.LlamaServer.ContextSize != 8192 {
		t.Fatal("Clone aliases context pointer")
	}
}
