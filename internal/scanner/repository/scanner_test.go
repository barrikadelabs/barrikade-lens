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
