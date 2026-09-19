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
	Tools             []Tool
}

type Tool struct {
	Name    string
	Enabled *bool
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
		current.Tools = mergeTools(current.Tools, server.Tools)
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
	server.Tools = declaredTools(config)
	return server
}

func declaredTools(config map[string]any) []Tool {
	tools := map[string]Tool{}
	for _, definition := range []struct {
		keys    []string
		enabled *bool
	}{
		{keys: []string{"tools", "declaredTools", "declared_tools"}},
		{keys: []string{"allowedTools", "allowed_tools", "enabledTools", "enabled_tools"}, enabled: boolPointer(true)},
		{keys: []string{"disabledTools", "disabled_tools"}, enabled: boolPointer(false)},
	} {
		for _, key := range definition.keys {
			value, ok := anyValue(config, key)
			if !ok {
				continue
			}
			collectTools(value, definition.enabled, tools)
		}
	}
	result := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, tool)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	if len(result) > 500 {
		result = result[:500]
	}
	return result
}

func collectTools(value any, enabled *bool, result map[string]Tool) {
	add := func(name string, state *bool) {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 200 || strings.ContainsAny(name, "\r\n\x00") {
			return
		}
		key := strings.ToLower(name)
		current, exists := result[key]
		if !exists || current.Enabled == nil {
			result[key] = Tool{Name: name, Enabled: state}
		}
	}
	switch typed := value.(type) {
	case string:
		add(typed, enabled)
	case []any:
		for _, item := range typed {
			switch candidate := item.(type) {
			case string:
				add(candidate, enabled)
			case map[string]any:
				name, _ := stringValue(candidate, "name", "id")
				state := enabled
				if declared, ok := boolValue(candidate, "enabled"); ok {
					state = boolPointer(declared)
				}
				add(name, state)
			}
		}
	case map[string]any:
		for name, raw := range typed {
			state := enabled
			if candidate, ok := raw.(map[string]any); ok {
				if declared, present := boolValue(candidate, "enabled"); present {
					state = boolPointer(declared)
				}
			}
			add(name, state)
		}
	}
}

func mergeTools(existing, additions []Tool) []Tool {
	values := map[string]Tool{}
	for _, tool := range append(existing, additions...) {
		key := strings.ToLower(tool.Name)
		current, exists := values[key]
		if !exists || current.Enabled == nil {
			values[key] = tool
		}
	}
	result := make([]Tool, 0, len(values))
	for _, tool := range values {
		result = append(result, tool)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	return result
}

func boolPointer(value bool) *bool { return &value }

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

func anyValue(object map[string]any, key string) (any, bool) {
	for present, value := range object {
		if normalizeKey(present) == normalizeKey(key) {
			return value, true
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
