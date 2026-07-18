package discovery

import "testing"

func TestMergeSnapshotsPreservesDuplicateEntityEvidence(t *testing.T) {
	left := NewSnapshot("org", "endpoint", SourceEndpoint, Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	right := NewSnapshot("org", "repository", SourceRepository, left.Collector)
	left.Evidence = []Evidence{{ID: "a", DetectorID: "a", Method: "path", Family: "installation", ObservedAt: left.ObservedAt}}
	right.Evidence = []Evidence{{ID: "b", DetectorID: "b", Method: "descriptor", Family: "configuration", ObservedAt: right.ObservedAt}}
	left.Entities = []Entity{{ID: "same", Kind: KindRuntime, Name: "Runtime", Attributes: map[string]any{"installed": true}, Confidence: ConfidenceLikely, EvidenceRefs: []string{"a"}}}
	right.Entities = []Entity{{ID: "same", Kind: KindRuntime, Name: "Runtime", Attributes: map[string]any{"configured": true}, Confidence: ConfidenceConfirmed, EvidenceRefs: []string{"b"}}}
	if err := MergeSnapshots(&left, right); err != nil {
		t.Fatal(err)
	}
	if len(left.Entities) != 1 || len(left.Evidence) != 2 {
		t.Fatalf("unexpected merge sizes: %d entities, %d evidence", len(left.Entities), len(left.Evidence))
	}
	entity := left.Entities[0]
	if entity.Confidence != ConfidenceConfirmed || len(entity.EvidenceRefs) != 2 || entity.Attributes["configured"] != true {
		t.Fatalf("duplicate entity evidence was lost: %#v", entity)
	}
	if err := left.Validate(); err != nil {
		t.Fatal(err)
	}
}
