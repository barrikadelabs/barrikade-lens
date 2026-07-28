package endpoint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/mcpconfig"
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
	if err := os.WriteFile(filepath.Join(configDir, "skills", "reviewer", "SKILL.md"), []byte("---\nname: reviewer\ndescription: Review changes\n---\nprivate instructions"), 0o600); err != nil {
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
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindRuntime && (entity.Attributes["configured"] == true || entity.Confidence != discovery.ConfidencePossible) {
			t.Fatalf("malformed configuration overstated runtime posture: %#v", entity)
		}
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
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Listeners: []detector.Listener{{ID: "fixture-mcp", Name: "Fixture MCP", Kind: "mcp_server", Port: 4444, Processes: []string{"fixture-server"}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: t.TempDir(), Hostname: "fixture", Pack: pack, ProcessNames: map[string]bool{"fixture-server": true}, ListeningPorts: map[int]bool{4444: true}, ListeningBindings: map[int]string{4444: "all_interfaces"}, DisableSystemInspection: true})
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

func TestKnownListenerDoesNotAttributeUnrelatedProcessOnSamePort(t *testing.T) {
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Listeners: []detector.Listener{{ID: "fixture-mcp", Name: "Fixture MCP", Kind: "mcp_server", Port: 4444, Processes: []string{"fixture-server"}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: t.TempDir(), Hostname: "fixture", Pack: pack, ProcessNames: map[string]bool{"unrelated": true}, ListeningPorts: map[int]bool{4444: true}, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindMCPServer {
			t.Fatalf("unrelated listener was misattributed as %#v", entity)
		}
	}
}

func TestKnownListenerRequiresMatchingSocketOwnerWhenAvailable(t *testing.T) {
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Listeners: []detector.Listener{{ID: "fixture-mcp", Name: "Fixture MCP", Kind: "mcp_server", Port: 4444, Processes: []string{"fixture-server"}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: t.TempDir(), Hostname: "fixture", Pack: pack, ProcessNames: map[string]bool{"fixture-server": true}, ListeningPorts: map[int]bool{4444: true}, ListeningProcesses: map[int]map[string]bool{4444: {"unrelated": true}}, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindMCPServer {
			t.Fatalf("listener was not owned by the matching process: %#v", entity)
		}
	}
}

func TestKnownListenerRecordsVerifiedSocketOwnership(t *testing.T) {
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Listeners: []detector.Listener{{ID: "fixture-mcp", Name: "Fixture MCP", Kind: "mcp_server", Port: 4444, Processes: []string{"fixture-server"}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: t.TempDir(), Hostname: "fixture", Pack: pack, ListeningPorts: map[int]bool{4444: true}, ListeningProcesses: map[int]map[string]bool{4444: {"fixture-server": true}}, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindMCPServer && entity.Attributes["listener_process_verified"] == true {
			return
		}
	}
	t.Fatal("verified listener ownership was not recorded")
}

func TestModelServerRequiresRuntimeProcessAndPort(t *testing.T) {
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{ID: "example", Name: "Example", Processes: []string{"example"}, ModelServers: []detector.Port{{Name: "Example API", Port: 4444}}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: t.TempDir(), Hostname: "fixture", Pack: pack, ProcessNames: map[string]bool{"unrelated": true}, ListeningPorts: map[int]bool{4444: true}, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindModelServer || entity.Name == "Example" {
			t.Fatalf("port-only evidence created an entity: %#v", entity)
		}
	}
}

func TestStatePathDoesNotClaimRuntimeInstalled(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".example"), 0o755); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{ID: "example", Name: "Example", Paths: []string{"~/.example"}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: home, Hostname: "fixture", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind != discovery.KindRuntime {
			continue
		}
		if entity.Attributes["state_present"] != true || entity.Attributes["installed"] != nil || entity.Confidence != discovery.ConfidencePossible {
			t.Fatalf("runtime state was overstated: %#v", entity)
		}
		return
	}
	t.Fatal("runtime state was not discovered")
}

func TestApplicationPathClaimsRuntimeInstalled(t *testing.T) {
	home := t.TempDir()
	installed := filepath.Join(home, "Example.app")
	if err := os.Mkdir(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{ID: "example", Name: "Example", InstallPaths: []string{installed}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: home, Hostname: "fixture", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		methods, _ := entity.Attributes["installation_methods"].([]string)
		if entity.Kind == discovery.KindRuntime && entity.Attributes["installed"] == true && slicesContain(methods, "application_path") {
			return
		}
	}
	t.Fatal("application installation was not discovered")
}

func TestVersionedIDEExtensionClaimsRuntimeInstalled(t *testing.T) {
	home := t.TempDir()
	extension := filepath.Join(home, ".vscode", "extensions", "example.agent-1.2.3")
	if err := os.MkdirAll(extension, 0o755); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "1", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{ID: "example", Name: "Example", InstallPaths: []string{"~/.vscode/extensions/example.agent-*"}}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", SourceID: "endpoint", HomeDir: home, Hostname: "fixture", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		methods, _ := entity.Attributes["installation_methods"].([]string)
		if entity.Kind == discovery.KindRuntime && entity.Attributes["installed"] == true && slicesContain(methods, "ide_extension") {
			return
		}
	}
	t.Fatal("versioned IDE extension was not discovered")
}

func TestJSONCConfigSupportsCommentsAndTrailingCommas(t *testing.T) {
	document, err := parseConfig("jsonc", []byte("{\n// comment\n\"model\": \"example\",\n\"url\": \"https://example.test/a//b\",\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	object := document.(map[string]any)
	if object["model"] != "example" || object["url"] != "https://example.test/a//b" {
		t.Fatalf("unexpected JSONC document: %#v", object)
	}
}

func TestMCPCredentialPresenceOnlyUsesSensitiveEnvironmentKeys(t *testing.T) {
	servers := mcpconfig.Find(map[string]any{"mcpServers": map[string]any{
		"metadata": map[string]any{"command": "example", "env": map[string]any{"CODEX_HOME": "/tmp/codex", "VERSION": "1"}},
		"secret":   map[string]any{"command": "example", "env": map[string]any{"API_TOKEN": "present"}},
	}})
	byName := map[string]mcpconfig.Server{}
	for _, server := range servers {
		byName[server.Name] = server
	}
	if byName["metadata"].CredentialPresent {
		t.Fatal("non-credential environment metadata was classified as a credential")
	}
	if !byName["secret"].CredentialPresent {
		t.Fatal("credential-shaped environment key was not detected")
	}
}

func TestEmptyKnownConfigIsResidualNotConfigured(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "empty.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "2", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{
		ID: "example", Name: "Example", Category: "agent_tool", Configs: []detector.Config{{Path: "~/empty.json", Format: "json", Scope: "user"}},
	}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", HomeDir: home, Hostname: "fixture", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindRuntime {
			if entity.Attributes["state_present"] != true || entity.Attributes["configured"] == true || entity.Confidence != discovery.ConfidencePossible {
				t.Fatalf("empty configuration overstated runtime posture: %#v", entity)
			}
			return
		}
	}
	t.Fatal("empty known configuration was not retained as residual evidence")
}

func TestPortableSkillRootRequiresSkillDescriptor(t *testing.T) {
	home := t.TempDir()
	validRoot := filepath.Join(home, ".agents", "skills", "code-review")
	noiseRoot := filepath.Join(home, ".agents", "skills", "notes")
	if err := os.MkdirAll(validRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(noiseRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(validRoot, "SKILL.md"), []byte("---\nname: code-review\ndescription: Review code\n---\nprivate instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "2", ID: "test", Version: "1", SkillRoots: []detector.SkillRoot{{ID: "portable", Name: "Portable", Paths: []string{"~/.agents/skills"}, Scope: "user"}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", HomeDir: home, Hostname: "fixture", Username: "alice", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	skills := []discovery.Entity{}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindSkill {
			skills = append(skills, entity)
		}
	}
	if len(skills) != 1 || skills[0].Name != "code-review" || skills[0].Confidence != discovery.ConfidenceConfirmed {
		t.Fatalf("portable skill detection was not precise: %#v", skills)
	}
	data, _ := json.Marshal(snapshot)
	if strings.Contains(string(data), "private instructions") {
		t.Fatal("skill body leaked into the snapshot")
	}
}

func TestMalformedSkillDescriptorDoesNotCreateSkillOrRuntime(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".example", "skills", "not-a-skill")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("ordinary project notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "2", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{
		ID: "example", Name: "Example", Category: "agent_tool", SkillRoots: []string{"~/.example/skills"},
	}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", HomeDir: home, Hostname: "fixture", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindSkill || entity.Kind == discovery.KindRuntime {
			t.Fatalf("malformed skill descriptor created an inventory entity: %#v", entity)
		}
	}
}

func TestPortableSkillRootFollowsLinkedSkillDirectoryWithoutWalkingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating directory symlinks requires additional Windows privileges")
	}
	home := t.TempDir()
	shared := filepath.Join(home, "shared", "code-review")
	root := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "SKILL.md"), []byte("---\nname: code-review\ndescription: Review code\n---\nprivate instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(root, "code-review")); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "2", ID: "test", Version: "1", SkillRoots: []detector.SkillRoot{{ID: "portable", Name: "Portable", Paths: []string{"~/.agents/skills"}, Scope: "user"}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", HomeDir: home, Hostname: "fixture", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	skills := 0
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindSkill && entity.Name == "code-review" {
			skills++
		}
	}
	if skills != 1 {
		t.Fatalf("expected one linked skill, got %d", skills)
	}
}

func TestKnownAgentRootFindsDefinitionWithoutRetainingInstructions(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".copilot", "agents")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: release-planner\n---\nsecret project instructions\n"
	if err := os.WriteFile(filepath.Join(root, "release-planner.agent.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := detector.Pack{SchemaVersion: "2", ID: "test", Version: "1", Runtimes: []detector.RuntimeSignature{{
		ID: "copilot", Name: "Copilot", Category: "agent_tool", AgentRoots: []string{"~/.copilot/agents"},
	}}}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", HomeDir: home, Hostname: "fixture", Username: "alice", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindAgent && entity.Name == "release-planner" && entity.Attributes["defined"] == true && entity.Confidence == discovery.ConfidenceConfirmed {
			found = true
		}
	}
	if !found {
		t.Fatal("custom agent definition was not discovered")
	}
	serialized, _ := json.Marshal(snapshot)
	if strings.Contains(string(serialized), "secret project instructions") {
		t.Fatal("agent instructions leaked into the snapshot")
	}
}

func TestIDEExtensionManifestsFindKnownAndFutureAgentTools(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".vscode", "extensions")
	fixtures := map[string]string{
		"known-1.0.0":    `{"name":"known","publisher":"acme","displayName":"Known extension","private":"secret body"}`,
		"future-1.0.0":   `{"name":"future-agent","publisher":"newco","displayName":"Future Agent","contributes":{"chatParticipants":[{"id":"future"}],"languageModelTools":[{"name":"lookup"}]},"description":"private instructions"}`,
		"ordinary-1.0.0": `{"name":"theme","publisher":"newco","displayName":"Ordinary theme","contributes":{"themes":[{"path":"theme.json"}]}}`,
	}
	for directory, manifest := range fixtures {
		path := filepath.Join(root, directory)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pack := detector.Pack{
		SchemaVersion: "2", ID: "test", Version: "1", ExtensionRoots: []string{"~/.vscode/extensions", "%USERPROFILE%/.vscode/extensions"},
		Runtimes: []detector.RuntimeSignature{{ID: "known", Name: "Known Product", Category: "agent_tool", ExtensionIDs: []string{"acme.known"}}},
	}
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", HomeDir: home, Hostname: "fixture", Username: "alice", Pack: pack, DisableSystemInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	runtimes := map[string]discovery.Entity{}
	tools := map[string]discovery.Entity{}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindRuntime {
			productID, _ := entity.Attributes["product_id"].(string)
			runtimes[productID] = entity
		}
		if entity.Kind == discovery.KindTool {
			extensionID, _ := entity.Attributes["extension_id"].(string)
			tools[extensionID] = entity
		}
	}
	if len(runtimes) != 1 || runtimes["known"].Confidence != discovery.ConfidenceConfirmed {
		t.Fatalf("known extension descriptor was not normalized: %#v", runtimes)
	}
	if len(tools) != 1 {
		t.Fatalf("ordinary extensions should not enter capability inventory: %#v", tools)
	}
	future := tools["newco.future-agent"]
	capabilities, _ := future.Attributes["agent_capabilities"].([]string)
	if future.Name != "Future Agent" || future.Confidence != discovery.ConfidenceConfirmed || !slicesContain(capabilities, "chat_participant") || !slicesContain(capabilities, "language_model_tool") {
		t.Fatalf("future agent-capable extension was not discovered generically: %#v", future)
	}
	serialized, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"secret body", "private instructions", home} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("extension discovery leaked %q", forbidden)
		}
	}
}

func TestMacApplicationNameFromProcessCommand(t *testing.T) {
	command := "/Applications/Antigravity IDE.app/Contents/MacOS/Electron"
	if got := macApplicationName(command); got != "Antigravity IDE" {
		t.Fatalf("macApplicationName()=%q", got)
	}
}

func TestParseDarwinListenerOwners(t *testing.T) {
	owners := parseDarwinListenerOwners("p10\ncControlCenter\nf1\nn*:5000\np20\ncollama\nf2\nn127.0.0.1:11434\n")
	if !owners[5000]["controlcenter"] || !owners[11434]["ollama"] {
		t.Fatalf("unexpected Darwin listener owners: %#v", owners)
	}
}

func TestParseLinuxListenerOwners(t *testing.T) {
	owners := parseLinuxListenerOwners("LISTEN 0 4096 127.0.0.1:11434 0.0.0.0:* users:((\"ollama\",pid=20,fd=3))\n")
	if !owners[11434]["ollama"] {
		t.Fatalf("unexpected Linux listener owners: %#v", owners)
	}
}

func slicesContain(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestClassifyListenerBindings(t *testing.T) {
	for input, expected := range map[string]string{"0.0.0.0": "all_interfaces", "::": "all_interfaces", "127.0.0.1": "loopback", "192.0.2.10": "interface"} {
		if got := classifyBinding(input); got != expected {
			t.Errorf("classifyBinding(%q)=%q, want %q", input, got, expected)
		}
	}
}
