package repository

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestRepositoryScanBuildsEvidenceGraphWithoutSourceContent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"dependencies":{"@langchain/core":"1.0.0"},"private":"repository-secret"}`)
	writeFixture(t, root, "agents/support-agent.yaml", "name: Support Agent\nmodel: example-model\nsystem_prompt: never serialize this prompt\n")
	writeFixture(t, root, ".mcp.json", `{"mcpServers":{"crm":{"url":"https://user:pass@api.example.test/mcp?token=never","env":{"API_KEY":"secret"}}}}`)
	writeFixture(t, root, "openapi.yaml", "openapi: 3.1.0\ninfo:\n  title: Example API\nservers:\n  - url: https://api.example.test/v1?key=never\npaths:\n  /tickets:\n    get:\n      operationId: listTickets\n")
	writeFixture(t, root, "arazzo.yaml", "arazzo: 1.1.0\ninfo:\n  title: Workflows\n  version: 1.0.0\nsourceDescriptions: []\nworkflows:\n  - workflowId: triageTicket\n    steps: []\n")
	writeFixture(t, root, ".well-known/agent-card.json", `{"name":"Support Card","url":"https://user:pass@agents.example.test/a2a?token=never","capabilities":{"streaming":true},"skills":[{"id":"triage","name":"Triage ticket","description":"private detail"}]}`)
	writeFixture(t, root, ".github/CODEOWNERS", "* @barrikadelabs/platform\n")

	snapshot, err := Scan(context.Background(), Options{
		OrganizationID: "org", Root: root, RepositoryURL: "https://user:password@github.com/acme/agents.git?token=nope", CommitSHA: strings.Repeat("a", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	for _, kind := range []discovery.EntityKind{discovery.KindRepository, discovery.KindAgent, discovery.KindFramework, discovery.KindMCPServer, discovery.KindAPIService, discovery.KindAPIOperation, discovery.KindWorkflow, discovery.KindSkill, discovery.KindUser} {
		if kinds[kind] == 0 {
			t.Errorf("expected %s entity", kind)
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, forbidden := range []string{"repository-secret", "never serialize this prompt", "private detail", "password", "token=nope", "?key=never", "?token=never", root, `"API_KEY":"secret"`} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("snapshot leaked %q", forbidden)
		}
	}
	if !strings.Contains(serialized, "https://api.example.test/v1") {
		t.Fatal("sanitized OpenAPI server missing")
	}
}

func TestRepositoryFileLimitMarksCoveragePartial(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{}`)
	writeFixture(t, root, "requirements.txt", "crewai==1.0")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root, MaxFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Coverage.Partial {
		t.Fatal("expected partial coverage")
	}
}

func TestFrameworkNamesInSourceStringsAreNotImports(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "detectors_test.go", "package fixture\n\nvar detectorNames = []string{\"langchain\", \"langgraph\", \"crewai\", \"smolagents\"}\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindFramework || entity.Kind == discovery.KindAgent {
			t.Fatalf("source string was treated as framework evidence: %#v", entity)
		}
	}
}

func TestDependencyMatchingHandlesModernManifestSyntaxWithoutSubstrings(t *testing.T) {
	for _, syntax := range []string{
		"langchain~=0.3",
		"langchain[openai]>=0.3",
		"langchain = \"^0.3\"",
		`"langchain": "^0.3"`,
		"langchain@^0.3:",
	} {
		if !packageMatch(strings.ToLower(syntax), "langchain") {
			t.Errorf("dependency syntax was missed: %q", syntax)
		}
	}
	for _, noise := range []string{"langchain-community==1", "my-langchain-wrapper = \"1\"", `"@acme/langchain": "1"`} {
		if packageMatch(strings.ToLower(noise), "langchain") {
			t.Errorf("package substring produced a false match: %q", noise)
		}
	}
}

func TestNPMMetadataWordsAreNotDependencies(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"example","keywords":["ai","langchain"],"description":"ai"}`)
	writeFixture(t, root, "package-lock.json", `{"name":"example","packages":{"":{"name":"example"}},"funding":{"url":"https://github.com/sponsors/ai"}}`)
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindFramework {
			t.Fatalf("npm metadata word created framework inventory: %#v", entity)
		}
	}

	dependencyRoot := t.TempDir()
	writeFixture(t, dependencyRoot, "package.json", `{"dependencies":{"ai":"^5.0.0"}}`)
	dependencySnapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: dependencyRoot})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range dependencySnapshot.Entities {
		if entity.Kind == discovery.KindFramework && entity.Name == "Vercel AI SDK" {
			return
		}
	}
	t.Fatal("declared npm AI SDK dependency was missed")
}

func TestFrameworkImportDoesNotManufactureAgent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "agent.py", "from langchain.agents import create_agent\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	if kinds[discovery.KindFramework] != 1 || kinds[discovery.KindAgent] != 0 {
		t.Fatalf("framework import was misclassified: %#v", kinds)
	}
}

func TestFrameworkImportsRespectSourceEcosystem(t *testing.T) {
	pythonRoot := t.TempDir()
	writeFixture(t, pythonRoot, "main.py", "import ai\n")
	pythonSnapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: pythonRoot})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range pythonSnapshot.Entities {
		if entity.Kind == discovery.KindFramework && entity.Name == "Vercel AI SDK" {
			t.Fatalf("Python package named ai was mistaken for the JavaScript AI SDK: %#v", entity)
		}
	}

	javascriptRoot := t.TempDir()
	writeFixture(t, javascriptRoot, "main.ts", "import { generateText } from \"ai\"\n")
	javascriptSnapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: javascriptRoot})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range javascriptSnapshot.Entities {
		if entity.Kind == discovery.KindFramework && entity.Name == "Vercel AI SDK" {
			return
		}
	}
	t.Fatal("JavaScript AI SDK import was not discovered")
}

func TestAgentInstructionsAreRepositoryContextNotAgentDefinition(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "AGENTS.md", "# Build instructions\nRun tests before committing.\n")
	writeFixture(t, root, "workflow.yaml", "name: release\nsteps:\n  - run: test\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindAgent {
			t.Fatalf("instruction or generic workflow file became an agent: %#v", entity)
		}
		if entity.Kind == discovery.KindRepository && entity.Attributes["agent_instructions_present"] != true {
			t.Fatalf("AGENTS.md was not retained as repository context: %#v", entity)
		}
	}
}

func TestAgentDescriptorRequiresAgentShape(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "agents/real-agent.yaml", "name: Support\nmodel: example\n")
	writeFixture(t, root, "agents/not-agent.yaml", "name: Deployment metadata\nregion: us-east\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	agents := []discovery.Entity{}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindAgent {
			agents = append(agents, entity)
		}
	}
	if len(agents) != 1 || agents[0].Name != "Support" {
		t.Fatalf("agent shape validation failed: %#v", agents)
	}
}

func TestCustomAgentMarkdownIsDefinitionNotInstructionNoise(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".github/agents/security.agent.md", "---\nname: Security reviewer\ntools: [read]\n---\nprivate system instructions\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindAgent && entity.Name == "Security reviewer" && entity.Confidence == discovery.ConfidenceConfirmed {
			data, _ := json.Marshal(snapshot)
			if strings.Contains(string(data), "private system instructions") {
				t.Fatal("custom agent body leaked into snapshot")
			}
			return
		}
	}
	t.Fatal("custom agent definition was not discovered")
}

func TestCurrentA2AInterfacesAndMCPServersShape(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".well-known/agent-card.json", `{"name":"Support","protocolVersion":"1.0","supportedInterfaces":[{"url":"https://agent.example.test/a2a","protocolBinding":"JSONRPC"}],"capabilities":{},"skills":[]}`)
	writeFixture(t, root, "mcp.json", `{"servers":{"catalog":{"type":"streamable-http","url":"https://catalog.example.test/mcp"}}}`)
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	foundAgent, foundMCP := false, false
	for _, entity := range snapshot.Entities {
		switch entity.Kind {
		case discovery.KindAgent:
			foundAgent = entity.Attributes["endpoint"] == "https://agent.example.test/a2a" && entity.Confidence == discovery.ConfidenceConfirmed
		case discovery.KindMCPServer:
			foundMCP = entity.Attributes["transport"] == "streamable_http" && entity.Confidence == discovery.ConfidenceConfirmed
		}
	}
	if !foundAgent || !foundMCP {
		t.Fatalf("current protocol shapes were not detected: %#v", snapshot.Entities)
	}
}

func TestRepositorySkillDescriptorUsesOpenFormat(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".agents/skills/review/SKILL.md", "---\nname: review\ndescription: Review code\n---\nprivate instructions\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindSkill && entity.Name == "review" && entity.Confidence == discovery.ConfidenceConfirmed {
			if entity.Attributes["descriptor_valid"] != true || entity.Attributes["descriptor_format"] != "agent_skills" || entity.Attributes["descriptor_relative"] != ".agents/skills/review/SKILL.md" || entity.Attributes["skill_scope"] != "repository" || entity.Attributes["declared_purpose"] != "Review code" {
				t.Fatalf("repository skill omitted safe descriptor facts: %#v", entity)
			}
			data, _ := json.Marshal(snapshot)
			if strings.Contains(string(data), "private instructions") {
				t.Fatal("skill body leaked into repository snapshot")
			}
			return
		}
	}
	t.Fatal("standards-based repository skill was not detected")
}

func TestMalformedMarkdownDescriptorsDoNotCreateAgentsOrSkills(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".github/agents/empty.agent.md", "")
	writeFixture(t, root, ".github/agents/broken.agent.md", "---\nname: [\n---\nbody\n")
	writeFixture(t, root, ".claude/agents/README.md", "# Documentation, not an agent\n")
	writeFixture(t, root, ".agents/skills/notes/SKILL.md", "ordinary notes\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindAgent || entity.Kind == discovery.KindSkill {
			t.Fatalf("malformed descriptor created an inventory entity: %#v", entity)
		}
	}
}

func TestSourceFileUnderKubernetesDirectoryIsNotADeployment(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/scanner/kubernetes/scanner.go", "package kubernetes\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindWorkload || entity.Kind == discovery.KindAgent {
			t.Fatalf("ordinary source path was treated as deployment evidence: %#v", entity)
		}
	}
}

func TestOperationalArtifactsDoNotManufactureGenericAgent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "openapi.yaml", "openapi: 3.1.0\ninfo:\n  title: Inventory API\npaths: {}\n")
	writeFixture(t, root, "kubernetes/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: inventory\n")
	writeFixture(t, root, ".mcp.json", `{"mcpServers":{"catalog":{"url":"https://catalog.example.test/mcp"}}}`)
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	if kinds[discovery.KindAgent] != 0 {
		t.Fatalf("operational artifacts manufactured an agent: %#v", kinds)
	}
	for _, kind := range []discovery.EntityKind{discovery.KindAPIService, discovery.KindWorkload, discovery.KindMCPServer} {
		if kinds[kind] != 1 {
			t.Fatalf("expected one %s entity, got %#v", kind, kinds)
		}
	}
}

func TestMalformedOpenAPIDocumentDoesNotCreateAPI(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "openapi.yaml", "openapi: definitely-not-a-version\ninfo:\n  title: Noise\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindAPIService || entity.Kind == discovery.KindAPIOperation {
			t.Fatalf("invalid OpenAPI marker created API inventory: %#v", entity)
		}
	}
}

func TestDeploymentArtifactsUseFactualEntityKinds(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".github/workflows/ci.yml", "name: CI\non: [push]\njobs: {}\n")
	writeFixture(t, root, "docker-compose.yml", "services:\n  hub:\n    image: example/hub\n  postgres:\n    image: postgres\n")
	writeFixture(t, root, "helm/templates/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: hub\n")
	writeFixture(t, root, "helm/templates/service.yaml", "apiVersion: v1\nkind: Service\nmetadata:\n  name: hub\n")
	writeFixture(t, root, "helm/values.yaml", "replicaCount: 2\n")
	writeFixture(t, root, "Dockerfile", "FROM scratch\n")
	snapshot, err := Scan(context.Background(), Options{OrganizationID: "org", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	if kinds[discovery.KindWorkflow] != 1 || kinds[discovery.KindWorkload] != 3 || kinds[discovery.KindAgent] != 0 {
		t.Fatalf("deployment artifacts were misclassified: %#v", kinds)
	}
}

func writeFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
