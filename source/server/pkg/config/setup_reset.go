package config

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// setupResetRoots is the audited setup ownership boundary. Entire runtime and
// model subtrees are owned; compaction and watchdog are deliberately not here.
var setupResetRoots = []string{
	"ollama_url", "open_runtime", "open_model", "embedding_model",
	"cloud_provider", "cloud_model", "cloud_api_key", "cloud_base_url",
	"cloud_profiles", "active_cloud_profile", "backup_cloud_profile",
	"secondary_cloud_profile", "secondary_backup_cloud_profile",
	"task_assignments", "secondary_redirect", "local_redirect", "locus_mode",
	"llama_server", "mistralrs", "models", "model_profiles",
}

// ResetSetupYAML returns a fresh single YAML document with setup-owned fields
// restored to current Defaults. Unrelated keys and nested feature preferences
// are retained. It performs no configuration loading, persistence or credential
// operations. Empty input is treated as a new installation.
//
// Aliases and merge keys are conservatively unsupported anywhere in the input:
// they can couple preserved state to replaced nodes. Non-string mapping keys,
// duplicate keys and invalid known-field types are also rejected. Formatting
// may be normalized; this is not a byte-preserving editor.
func ResetSetupYAML(input []byte) ([]byte, error) {
	dec := yaml.NewDecoder(bytes.NewReader(input))
	var doc yaml.Node
	err := dec.Decode(&doc)
	empty := err == io.EOF
	if err != nil && !empty {
		return nil, fmt.Errorf("decode setup config: %w", err)
	}
	if !empty {
		var extra yaml.Node
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("setup config must contain exactly one YAML document")
		}
	}
	var defaults yaml.Node
	if err := defaults.Encode(Defaults()); err != nil {
		return nil, fmt.Errorf("encode setup defaults: %w", err)
	}
	if empty {
		return yaml.Marshal(&defaults)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("setup config root must be a mapping")
	}
	root := doc.Content[0]
	if err := validateSetupNode(root); err != nil {
		return nil, err
	}
	// Decode only in memory to reject incompatible known-field shapes before any
	// replacement can hide them. Unknown keys remain in the original node tree.
	var check Config
	if err := root.Decode(&check); err != nil {
		// yaml.TypeError includes snippets of input values. Config may contain
		// legacy plaintext credentials, so never echo parser values to the CLI.
		return nil, fmt.Errorf("invalid setup config types; expected types must match the configuration schema")
	}
	for _, key := range setupResetRoots {
		resetSetupKey(root, &defaults, key)
	}
	for _, field := range []struct{ parent, key string }{{"compaction", "summarizer_model"}, {"watchdog", "model"}} {
		node := setupMappingValue(root, field.parent)
		if node == nil {
			continue
		}
		if node.Tag == "!!null" {
			// A null feature map has no preferences to preserve. Omission retains the
			// ordinary loader's defaults rather than materializing zero preferences.
			resetSetupKey(root, &yaml.Node{Kind: yaml.MappingNode}, field.parent)
			continue
		}
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("setup config %s must be a mapping", field.parent)
		}
		resetSetupKey(node, setupMappingValue(&defaults, field.parent), field.key)
	}
	return yaml.Marshal(&doc)
}

func validateSetupNode(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode {
		return fmt.Errorf("setup config aliases are unsupported (line %d)", n.Line)
	}
	if n.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				return fmt.Errorf("setup config requires string keys, without merges (line %d)", k.Line)
			}
			if seen[k.Value] {
				return fmt.Errorf("setup config has duplicate key at line %d", k.Line)
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if err := validateSetupNode(c); err != nil {
			return err
		}
	}
	return nil
}

func setupMappingValue(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// Omitted default fields must be removed, not retained: many legacy fields use
// omitempty and would otherwise resurrect profiles during normal Load migration.
func resetSetupKey(dst, defaults *yaml.Node, key string) {
	value := setupMappingValue(defaults, key)
	for i := 0; i < len(dst.Content); i += 2 {
		if dst.Content[i].Value != key {
			continue
		}
		if value == nil {
			dst.Content = append(dst.Content[:i], dst.Content[i+2:]...)
		} else {
			dst.Content[i+1] = value
		}
		return
	}
	if value != nil {
		dst.Content = append(dst.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	}
}
