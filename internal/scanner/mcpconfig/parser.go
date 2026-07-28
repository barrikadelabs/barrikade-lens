// Package mcpconfig extracts MCP server declarations from already-parsed
// configuration documents. It recognizes the common client shapes without
// retaining commands, arguments, headers, or environment values.
package mcpconfig

import (
	"fmt"
	"sort"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type Server struct {
	Name              string
	Transport         string
	URL               string
	Enabled           *bool
	EnvironmentKeys   []string
	CredentialPresent bool
}

// Find returns normalized MCP server declarations. A generic "servers"
// object is accepted only when its children have an MCP-shaped configuration;
// this avoids treating unrelated application server lists as MCP.
func Find(document any) []Server {
	servers := map[string]Server{}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := normalizeKey(key)
				if normalized == "mcpservers" || normalized == "servers" && looksLikeServerCollection(child) {
					extractCollection(child, servers)
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(document)
	result := make([]Server, 0, len(servers))
	for _, server := range servers {
		result = append(result, server)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].URL < result[j].URL
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func extractCollection(value any, result map[string]Server) {
	switch typed := value.(type) {
	case map[string]any:
		for name, raw := range typed {
			config, ok := raw.(map[string]any)
			if !ok || !looksLikeServer(config) {
				continue
			}
			addServer(result, serverFrom(strings.TrimSpace(name), config))
		}
	case []any:
		for _, raw := range typed {
			config, ok := raw.(map[string]any)
			if !ok || !looksLikeServer(config) {
				continue
			}
			name, _ := stringValue(config, "name", "id")
			addServer(result, serverFrom(name, config))
		}
	}
}

func addServer(result map[string]Server, server Server) {
	if server.Name == "" || len(server.Name) > 500 {
		return
	}
	key := strings.ToLower(server.Name) + "\x00" + server.URL
	if current, exists := result[key]; exists {
		current.EnvironmentKeys = union(current.EnvironmentKeys, server.EnvironmentKeys)
		current.CredentialPresent = current.CredentialPresent || server.CredentialPresent
		if current.Enabled == nil {
			current.Enabled = server.Enabled
		}
		result[key] = current
		return
	}
	result[key] = server
}

func serverFrom(name string, config map[string]any) Server {
	server := Server{Name: name, Transport: "stdio"}
	if endpoint, ok := stringValue(config, "url", "endpoint", "serverUrl", "server_url"); ok {
		server.URL = endpoint
		server.Transport = "http"
	}
	if transport, ok := stringValue(config, "transport", "type"); ok {
		server.Transport = normalizeTransport(transport)
	}
	if enabled, ok := boolValue(config, "enabled"); ok {
		server.Enabled = &enabled
	}
	if disabled, ok := boolValue(config, "disabled"); ok {
		enabled := !disabled
		server.Enabled = &enabled
	}
	if env, ok := mapValue(config, "env", "environment"); ok {
		for key, value := range env {
			server.EnvironmentKeys = append(server.EnvironmentKeys, key)
			if discovery.IsSensitiveKey(key) && strings.TrimSpace(fmt.Sprint(value)) != "" {
				server.CredentialPresent = true
			}
		}
	}
	if headers, ok := mapValue(config, "headers", "httpHeaders", "http_headers"); ok {
		for key, value := range headers {
			if discovery.IsSensitiveKey(key) && strings.TrimSpace(fmt.Sprint(value)) != "" {
				server.CredentialPresent = true
			}
		}
	}
	server.EnvironmentKeys = union(nil, server.EnvironmentKeys)
	return server
}

func looksLikeServerCollection(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, raw := range typed {
			if config, ok := raw.(map[string]any); ok && looksLikeServer(config) {
				return true
			}
		}
	case []any:
		for _, raw := range typed {
			if config, ok := raw.(map[string]any); ok && looksLikeServer(config) {
				return true
			}
		}
	}
	return false
}

func looksLikeServer(config map[string]any) bool {
	if _, ok := stringValue(config, "command", "url", "endpoint", "serverUrl", "server_url"); ok {
		return true
	}
	if transport, ok := stringValue(config, "transport", "type"); ok {
		switch normalizeTransport(transport) {
		case "stdio", "http", "sse", "streamable_http":
			return true
		}
	}
	return false
}

func normalizeTransport(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "streamable-http", "streamable_http":
		return "streamable_http"
	case "sse":
		return "sse"
	case "http", "stdio":
		return value
	default:
		return value
	}
}

func normalizeKey(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func stringValue(object map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		for present, value := range object {
			if normalizeKey(present) == normalizeKey(key) {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text), true
				}
			}
		}
	}
	return "", false
}

func boolValue(object map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		for present, value := range object {
			if normalizeKey(present) == normalizeKey(key) {
				result, ok := value.(bool)
				return result, ok
			}
		}
	}
	return false, false
}

func mapValue(object map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		for present, value := range object {
			if normalizeKey(present) == normalizeKey(key) {
				result, ok := value.(map[string]any)
				return result, ok
			}
		}
	}
	return nil, false
}

func union(existing, additions []string) []string {
	seen := map[string]struct{}{}
	for _, value := range append(existing, additions...) {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
