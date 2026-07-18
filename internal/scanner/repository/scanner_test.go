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

func TestFrameworkImportStillCreatesAgentGraph(t *testing.T) {
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
	if kinds[discovery.KindFramework] != 1 || kinds[discovery.KindAgent] != 1 {
		t.Fatalf("framework import graph missing: %#v", kinds)
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
