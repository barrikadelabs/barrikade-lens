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
