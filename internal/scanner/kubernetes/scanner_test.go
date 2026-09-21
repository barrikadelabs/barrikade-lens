package kubernetes

import (
	"encoding/json"
	"fmt"
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

func TestKubernetesCorrelatesWorkloadToRepositoryUsingExplicitMetadata(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	inventory := Inventory{ClusterID: "cluster", ClusterName: "test", Workloads: []Workload{{
		UID: "worker", Namespace: "agents", Kind: "Deployment", Name: "worker",
		Labels: map[string]string{
			"barrikade.ai/agent":                "true",
			"org.opencontainers.image.source":   "git@github.com:acme/support.git",
			"org.opencontainers.image.revision": strings.Repeat("b", 40),
		},
		Images: []string{"registry.example/support@" + digest}, Running: true,
	}}}
	snapshot, err := Scan(Options{OrganizationID: "org", Full: true, Inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	repositoryID := discovery.StableID("org", discovery.KindRepository, "https://github.com/acme/support")
	foundRepository, foundRelationship, foundDigest := false, false, false
	for _, entity := range snapshot.Entities {
		if entity.ID == repositoryID && entity.Kind == discovery.KindRepository {
			foundRepository = true
		}
		if entity.Kind == discovery.KindWorkload {
			for _, value := range entity.Attributes["image_digests"].([]string) {
				foundDigest = foundDigest || value == digest
			}
		}
	}
	for _, relationship := range snapshot.Relationships {
		if relationship.Kind == discovery.RelationshipDefinedIn && relationship.To == repositoryID && relationship.Attributes["correlation_basis"] == "explicit_repository_label" {
			foundRelationship = true
		}
	}
	if !foundRepository || !foundRelationship || !foundDigest {
		t.Fatalf("cross-surface correlation was incomplete: repository=%v relationship=%v digest=%v", foundRepository, foundRelationship, foundDigest)
	}
}

func TestKubernetesReportsDeniedAndParseFailureCoverage(t *testing.T) {
	inventory := Inventory{
		ClusterID: "cluster", ClusterName: "test",
		Workloads:      []Workload{{UID: "worker", Namespace: "agents", Kind: "Deployment", Name: "worker", ConfigMapRefs: []string{"mcp"}}},
		ConfigMaps:     map[string]ConfigMap{"agents/mcp": {Namespace: "agents", Name: "mcp", Data: map[string]string{"mcp.json": "{broken"}}},
		ResourceErrors: []ResourceError{{Resource: "customresourcedefinitions", Denied: true}},
	}
	snapshot, err := Scan(Options{OrganizationID: "org", Full: true, Inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Coverage.Partial || snapshot.Coverage.DetectorsFailed < 2 || snapshot.Coverage.LocationsDenied != 1 {
		t.Fatalf("partial coverage was not explicit: %#v", snapshot.Coverage)
	}
	codes := map[string]bool{}
	for _, scanError := range snapshot.Errors {
		codes[scanError.Code] = true
	}
	if !codes["referenced_configmap_parse_failed"] || !codes["kubernetes_resource_denied"] {
		t.Fatalf("safe failure codes missing: %s", fmt.Sprint(codes))
	}
}

func TestServiceIngressChainKeepsEvidence(t *testing.T) {
	inventory := Inventory{ClusterID: "cluster", ClusterName: "test",
		Workloads: []Workload{{UID: "worker", Namespace: "agents", Kind: "Deployment", Name: "worker", Labels: map[string]string{"app": "worker"}}},
		Services: []Service{
			{UID: "service", Namespace: "agents", Kind: "Service", Name: "worker", Selector: map[string]string{"app": "worker"}},
			{UID: "ingress", Namespace: "agents", Kind: "Ingress", Name: "public", Hosts: []string{"agents.example.test"}, Backends: []string{"worker"}},
		},
	}
	snapshot, err := Scan(Options{OrganizationID: "org", Full: true, Inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	exposes := 0
	for _, relationship := range snapshot.Relationships {
		if relationship.Kind == discovery.RelationshipExposes {
			exposes++
			if len(relationship.EvidenceRefs) == 0 {
				t.Fatalf("exposure edge has no evidence: %#v", relationship)
			}
		}
	}
	if exposes != 2 {
		t.Fatalf("expected workload→service→ingress chain, got %d exposure edges", exposes)
	}
}
