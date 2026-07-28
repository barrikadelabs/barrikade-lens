package mcpconfig

import "testing"

func TestFindRecognizesEstablishedAndEmergingClientShapes(t *testing.T) {
	document := map[string]any{
		"mcpServers": map[string]any{
			"legacy": map[string]any{"command": "npx", "env": map[string]any{"API_TOKEN": "secret", "MODE": "safe"}},
		},
		"mcp": map[string]any{"servers": map[string]any{
			"remote": map[string]any{"type": "streamable-http", "url": "https://example.test/mcp", "headers": map[string]any{"Authorization": "secret"}},
		}},
	}
	servers := Find(document)
	if len(servers) != 2 {
		t.Fatalf("got %d servers: %#v", len(servers), servers)
	}
	byName := map[string]Server{}
	for _, server := range servers {
		byName[server.Name] = server
	}
	if byName["legacy"].Transport != "stdio" || !byName["legacy"].CredentialPresent || len(byName["legacy"].EnvironmentKeys) != 2 {
		t.Fatalf("legacy server was not normalized: %#v", byName["legacy"])
	}
	if byName["remote"].Transport != "streamable_http" || byName["remote"].URL == "" || !byName["remote"].CredentialPresent {
		t.Fatalf("remote server was not normalized: %#v", byName["remote"])
	}
}

func TestFindRejectsUnrelatedServersObject(t *testing.T) {
	document := map[string]any{"servers": map[string]any{"production": map[string]any{"host": "db.example.test", "port": 5432}}}
	if servers := Find(document); len(servers) != 0 {
		t.Fatalf("unrelated servers were treated as MCP: %#v", servers)
	}
}

func TestFindRejectsEmptyOrMetadataOnlyMCPEntries(t *testing.T) {
	document := map[string]any{"mcpServers": map[string]any{
		"empty-command": map[string]any{"command": ""},
		"metadata-only": map[string]any{"env": map[string]any{"API_TOKEN": "secret"}},
	}}
	if servers := Find(document); len(servers) != 0 {
		t.Fatalf("invalid MCP entries were accepted: %#v", servers)
	}
}

func TestFindSupportsArrayServerDeclarations(t *testing.T) {
	document := map[string]any{"mcpServers": []any{map[string]any{"name": "catalog", "url": "https://catalog.example.test/mcp"}}}
	servers := Find(document)
	if len(servers) != 1 || servers[0].Name != "catalog" || servers[0].Transport != "http" {
		t.Fatalf("array declaration was not recognized: %#v", servers)
	}
}
