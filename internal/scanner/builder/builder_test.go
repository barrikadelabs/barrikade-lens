package builder

import (
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestConfidenceAggregation(t *testing.T) {
	snapshot := discovery.NewSnapshot("org", "source", discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	b := New(snapshot)
	config := b.AddEvidence(Observation{DetectorID: "runtime", DetectorVersion: "1", Method: "path", Family: "configuration", Specificity: "high", Locator: "a"})
	process := b.AddEvidence(Observation{DetectorID: "runtime", DetectorVersion: "1", Method: "process", Family: "process", Specificity: "high", Locator: "b"})
	id := b.AddEntity(discovery.KindRuntime, "runtime", "Runtime", nil, config)
	if got := b.Snapshot.Entities[0].Confidence; got != discovery.ConfidenceLikely {
		t.Fatalf("one evidence family should be likely, got %s", got)
	}
	b.AddEntity(discovery.KindRuntime, "runtime", "Runtime", map[string]any{"running_at_scan": true}, process)
	if b.Snapshot.Entities[0].ID != id || b.Snapshot.Entities[0].Confidence != discovery.ConfidenceConfirmed {
		t.Fatalf("independent high-specificity evidence should confirm entity: %#v", b.Snapshot.Entities[0])
	}
}

func TestEntityAttributeStringListsAreUnioned(t *testing.T) {
	snapshot := discovery.NewSnapshot("org", "source", discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	b := New(snapshot)
	b.AddEntity(discovery.KindRuntime, "runtime", "Runtime", map[string]any{"installation_methods": []string{"ide_extension"}})
	b.AddEntity(discovery.KindRuntime, "runtime", "Runtime", map[string]any{"installation_methods": []string{"executable_path", "ide_extension"}})
	methods, ok := b.Snapshot.Entities[0].Attributes["installation_methods"].([]string)
	if !ok || len(methods) != 2 || methods[0] != "executable_path" || methods[1] != "ide_extension" {
		t.Fatalf("installation methods were not normalized: %#v", methods)
	}
}
