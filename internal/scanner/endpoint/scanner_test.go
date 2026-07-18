package endpoint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestScanFindsRuntimeMCPModelsAndSkillsWithoutLeakingValues(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".example")
	if err := os.MkdirAll(filepath.Join(configDir, "skills", "reviewer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cache", "huggingface", "hub", "models--acme--reviewer"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"mcpServers":{"github":{"url":"https://user:pass@mcp.example.test/v1?api_key=do-not-serialize","env":{"API_TOKEN":"super-secret"}}},"model":"example-model"}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "skills", "reviewer", "SKILL.md"), []byte("private instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{
		ID: "example", Name: "Example", Processes: []string{"example"}, Paths: []string{"~/.example"},
		Configs:    []detector.Config{{Path: "~/.example/config.json", Format: "json", Scope: "user"}},
		SkillRoots: []string{"~/.example/skills"}, ModelServers: []detector.Port{{Name: "Example Models", Port: 4444}},
	}}, ModelCaches: []detector.ModelCache{{ID: "huggingface-cache", Name: "Hugging Face Hub Cache", Paths: []string{"~/.cache/huggingface/hub"}, Layout: "huggingface"}}}
	snapshot, err := Scan(context.Background(), Options{
		OrganizationID: "org", SourceID: "endpoint", HomeDir: home, Hostname: "fixture", Username: "alice",
		Pack: pack, ProcessNames: map[string]bool{"example": true}, ListeningPorts: map[int]bool{4444: true}, DisableSystemInspection: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	for kind, count := range map[discovery.EntityKind]int{discovery.KindRuntime: 1, discovery.KindMCPServer: 1, discovery.KindModel: 2, discovery.KindModelServer: 1, discovery.KindSkill: 1} {
		if kinds[kind] != count {
			t.Errorf("expected %d %s entities, got %d", count, kind, kinds[kind])
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, forbidden := range []string{"super-secret", "do-not-serialize", "private instructions", home, "user:pass"} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("snapshot leaked %q", forbidden)
		}
	}
	if !strings.Contains(serialized, "https://mcp.example.test/v1") {
		t.Fatal("sanitized MCP endpoint missing")
	}
}

func TestMalformedKnownConfigProducesPartialCoverage(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{
		ID: "broken", Name: "Broken", Configs: []detector.Config{{Path: "~/broken.json", Format: "json", Scope: "user"}},
	}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: home, Hostname: "fixture", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Coverage.Partial || len(snapshot.Errors) != 1 {
		t.Fatalf("expected partial coverage: %#v", snapshot.Coverage)
	}
}

func TestExecutablePresenceCountsAsInstalledWithoutRunning(t *testing.T) {
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{ID: "example", Name: "Example", Processes: []string{"example"}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: t.TempDir(), Hostname: "fixture", Pack: pack, ExecutableNames: map[string]bool{"example": true}, ProcessNames: map[string]bool{}, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindRuntime && entity.Attributes["installed"] == true && entity.Attributes["running_at_scan"] == nil {
			return
		}
	}
	t.Fatal("installed executable was not reported factually")
}

func TestKnownListenerReportsFactualBindingWithoutProbing(t *testing.T) {
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Listeners: []detector.Listener{{ID: "fixture-mcp", Name: "Fixture MCP", Kind: "mcp_server", Port: 4444}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: t.TempDir(), Hostname: "fixture", Pack: pack, ListeningPorts: map[int]bool{4444: true}, ListeningBindings: map[int]string{4444: "all_interfaces"}, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindMCPServer && entity.Attributes["binding"] == "all_interfaces" && entity.Attributes["transport"] == "tcp" {
			return
		}
	}
	t.Fatal("known listener binding was not reported")
}

func TestClassifyListenerBindings(t *testing.T) {
	for input, expected := range map[string]string{"0.0.0.0": "all_interfaces", "::": "all_interfaces", "127.0.0.1": "loopback", "192.0.2.10": "interface"} {
		if got := classifyBinding(input); got != expected {
			t.Errorf("classifyBinding(%q)=%q, want %q", input, got, expected)
		}
	}
}
