package mcptopology

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/mcpconfig"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestAddBuildsStableEvidenceBackedTopology(t *testing.T) {
	snapshot := discovery.NewTargetSnapshot("org", "source", "target", discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	b := builder.New(snapshot)
	ref := b.AddEvidence(builder.Observation{DetectorID: "fixture", DetectorVersion: "1", Method: "descriptor", Family: "mcp_configuration", Specificity: "high", Locator: "safe", Authoritative: true})
	owner := b.AddEntity(discovery.KindRuntime, "target:runtime:codex", "Codex", nil, ref)
	enabled := true
	result := Add(b, Context{LocalCanonicalPrefix: "target:local", SourceSurface: discovery.SourceEndpoint}, owner, mcpconfig.Server{
		Name: "CRM", Transport: "streamable_http", URL: "https://user:pass@api.example.test/mcp?token=never", Enabled: &enabled,
		Tools: []mcpconfig.Tool{{Name: "search_records"}, {Name: "update_record", Enabled: &enabled}},
	}, ref)
	finished, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerID == "" || result.DestinationID == "" || len(result.ToolIDs) != 2 {
		t.Fatalf("topology was incomplete: %#v", result)
	}
	if len(finished.Relationships) != 4 {
		t.Fatalf("got %d relationships, want owner→server, two tools, and destination", len(finished.Relationships))
	}
	for _, relationship := range finished.Relationships {
		if relationship.Surface != discovery.SourceEndpoint || relationship.ObservationState != discovery.ObservationDeclared || relationship.ObservedAt == "" || len(relationship.EvidenceRefs) == 0 {
			t.Fatalf("relationship provenance is incomplete: %#v", relationship)
		}
	}
	encoded, _ := json.Marshal(finished)
	for _, forbidden := range []string{"user:pass", "token=never"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("topology leaked %q", forbidden)
		}
	}
}

func TestRemoteServerCanonicalConvergesAcrossSurfaces(t *testing.T) {
	server := mcpconfig.Server{Name: "different display names do not matter", Transport: "http", URL: "https://api.example.test/mcp"}
	ids := []string{}
	for _, surface := range []discovery.SourceType{discovery.SourceEndpoint, discovery.SourceRepository, discovery.SourceKubernetes} {
		snapshot := discovery.NewTargetSnapshot("org", "source-"+string(surface), "target-"+string(surface), surface, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
		b := builder.New(snapshot)
		ref := b.AddEvidence(builder.Observation{DetectorID: "fixture", DetectorVersion: "1", Method: "descriptor", Family: "mcp_configuration", Specificity: "high", Authoritative: true})
		ids = append(ids, Add(b, Context{LocalCanonicalPrefix: "surface-specific", SourceSurface: surface}, "", server, ref).ServerID)
	}
	if ids[0] != ids[1] || ids[1] != ids[2] {
		t.Fatalf("remote MCP identity did not converge: %#v", ids)
	}
}
