package localruntime

import (
	"net"
	"net/url"
	"strings"

	"cercano/source/server/pkg/config"
)

func EndpointsFromConfig(cfg config.Config) []EndpointRecord {
	endpoints := []EndpointRecord{
		{
			ID:          "ollama",
			Kind:        "ollama",
			DisplayName: "Ollama",
			BaseURL:     redactURL(cfg.OllamaURL),
			Scope:       endpointScope(cfg.OllamaURL, "local"),
			State:       StateUnknown,
			ActiveRoles: []string{"local_inference", "embeddings"},
			Models:      uniqueNonEmpty(cfg.OpenChatModel(), cfg.OpenEmbeddingModel()),
			AuthState:   "none",
		},
	}

	if cfg.CloudProvider != "" || cfg.CloudBaseURL != "" {
		kind := strings.ToLower(strings.TrimSpace(cfg.CloudProvider))
		if kind == "" {
			kind = "cloud"
		}
		if cfg.CloudBaseURL != "" {
			if kind == "anthropic" {
				kind = "anthropic_proxy"
			} else {
				kind = "openai_compatible"
			}
		}

		baseURL := cfg.CloudBaseURL
		if baseURL == "" {
			baseURL = "provider default"
		}
		authState := "missing"
		if cfg.CloudAPIKey != "" || cfg.CloudBaseURL != "" {
			authState = "configured"
		}

		endpoints = append(endpoints, EndpointRecord{
			ID:          "cloud:" + kind,
			Kind:        kind,
			DisplayName: cloudDisplayName(cfg.CloudProvider, cfg.CloudBaseURL),
			BaseURL:     redactURL(baseURL),
			Scope:       endpointScope(baseURL, "cloud"),
			State:       StateUnknown,
			ActiveRoles: []string{"cloud_fallback"},
			Models:      uniqueNonEmpty(cfg.CloudModel),
			AuthState:   authState,
		})
	}

	return endpoints
}

func cloudDisplayName(provider, baseURL string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "Cloud"
	}
	if baseURL != "" {
		return provider + " proxy"
	}
	return provider
}

func uniqueNonEmpty(values ...string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.UserPassword("redacted", "redacted")
	return u.String()
}

func endpointScope(raw, fallback string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fallback
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "local"
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return "local"
		}
		if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return "lan"
		}
		return "remote"
	}
	if strings.HasSuffix(host, ".local") {
		return "lan"
	}
	if fallback == "cloud" {
		return "cloud"
	}
	return "remote"
}
