package kubernetes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestKubernetesSnapshotContainsMetadataNotConfigValues(t *testing.T) {
	inventory := Inventory{ClusterID: "uid-cluster", ClusterName: "test", Workloads: []Workload{{UID: "uid-deployment", Namespace: "agents", Kind: "Deployment", Name: "claude-worker", Labels: map[string]string{"app": "claude-worker"}, Images: []string{"registry.example/claude-agent@sha256:123"}, Commands: []string{"/app/claude --token never"}, EnvironmentKeys: []string{"OPENAI_API_KEY"}, ConfigMapRefs: []string{"agent-config"}, MountNames: []string{"config"}, Running: true}}, Services: []Service{{UID: "uid-service", Namespace: "agents", Kind: "Service", Name: "claude", Hosts: []string{"agents.example.test"}, Ports: []int{8080}, Selector: map[string]string{"app": "claude-worker"}}}, ConfigMaps: map[string]ConfigMap{"agents/agent-config": {Namespace: "agents", Name: "agent-config", Data: map[string]string{"mcp.json": `{"mcpServers":{"crm":{"url":"https://user:pass@api.example.test/mcp?token=secret"}},"prompt":"private instructions"}`}}}}
	snapshot, err := Scan(Options{OrganizationID: "org", SourceID: "cluster-source", Full: true, Sequence: 1, Inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	for _, kind := range []discovery.EntityKind{discovery.KindCluster, discovery.KindWorkload, discovery.KindAgent, discovery.KindRuntime, discovery.KindMCPServer, discovery.KindAPIService} {
		if kinds[kind] == 0 {
			t.Errorf("missing %s", kind)
		}
	}
	data, _ := json.Marshal(snapshot)
	serialized := string(data)
	for _, forbidden := range []string{"never", "secret", "private instructions", "user:pass", "?token"} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("leaked %q", forbidden)
		}
	}
	if !strings.Contains(serialized, "OPENAI_API_KEY") {
		t.Fatal("environment key name should be retained")
	}
}

func TestSupportingRuntimeDoesNotManufactureKubernetesAgent(t *testing.T) {
	inventory := Inventory{
		ClusterID: "cluster", ClusterName: "test",
		Workloads: []Workload{{UID: "web", Namespace: "default", Kind: "Deployment", Name: "web", Images: []string{"node:24"}, Commands: []string{"node server.js"}, Running: true}},
	}
	snapshot, err := Scan(Options{OrganizationID: "org", Full: true, Inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	if kinds[discovery.KindRuntime] != 1 || kinds[discovery.KindAgent] != 0 {
		t.Fatalf("supporting runtime created an autonomous agent: %#v", kinds)
	}
}

func TestExplicitAgentMetadataCreatesKubernetesAgent(t *testing.T) {
	inventory := Inventory{
		ClusterID: "cluster", ClusterName: "test",
		Workloads: []Workload{{
			UID: "worker", Namespace: "agents", Kind: "Deployment", Name: "worker",
			Labels: map[string]string{"barrikade.ai/agent": "true"}, Images: []string{"registry.example/future-runtime:1"}, Running: true,
		}},
	}
	snapshot, err := Scan(Options{OrganizationID: "org", Full: true, Inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindAgent {
			if entity.Confidence != discovery.ConfidenceConfirmed {
				t.Fatalf("explicit agent label should be authoritative: %#v", entity)
			}
			return
		}
	}
	t.Fatal("explicit agent metadata did not create an agent without a known runtime")
}

func TestConfigMapMCPDiscoveryDoesNotRequireKnownRuntime(t *testing.T) {
	inventory := Inventory{
		ClusterID: "cluster", ClusterName: "test",
		Workloads: []Workload{{
			UID: "worker", Namespace: "default", Kind: "Deployment", Name: "worker",
			Images: []string{"registry.example/future-runtime:1"}, ConfigMapRefs: []string{"mcp"},
		}},
		ConfigMaps: map[string]ConfigMap{"default/mcp": {
			Namespace: "default", Name: "mcp", Data: map[string]string{"mcp.json": `{"servers":{"docs":{"type":"http","url":"https://mcp.example.test"}}}`},
		}},
	}
	snapshot, err := Scan(Options{OrganizationID: "org", Full: true, Inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindMCPServer && entity.Name == "docs" {
			return
		}
	}
	t.Fatal("valid MCP configuration was missed because its workload used an unknown runtime")
}

func TestImageMatchingUsesRepositoryBoundaries(t *testing.T) {
	for _, test := range []struct {
		image, expected string
		match           bool
	}{
		{"docker.io/ollama/ollama:latest", "ollama/ollama", true},
		{"ghcr.io/acme/vllm-openai:1", "vllm", false},
		{"ghcr.io/vllm/vllm-openai:1", "vllm/vllm-openai", true},
		{"ghcr.io/acme/notollama:1", "ollama", false},
	} {
		if got := imageMatches(test.image, test.expected); got != test.match {
			t.Errorf("imageMatches(%q,%q)=%v, want %v", test.image, test.expected, got, test.match)
		}
	}
}
