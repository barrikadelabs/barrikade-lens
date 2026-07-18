package discovery

import (
	"strings"
	"testing"
	"time"
)

func validSnapshot() Snapshot {
	s := NewSnapshot("org-test", "source-test", SourceEndpoint, Collector{ID: "test", Name: "test", Version: "dev", Mode: "standalone"})
	evidenceID := EvidenceID("source-test", "test", "config", "hash:locator", "hash:content")
	s.Evidence = append(s.Evidence, Evidence{
		ID: evidenceID, DetectorID: "test", DetectorVersion: "1", Method: "descriptor",
		Family: "configuration", Specificity: "high", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	entityID := StableID("org-test", KindRuntime, "test-runtime")
	s.Entities = append(s.Entities, Entity{
		ID: entityID, Kind: KindRuntime, Name: "Test Runtime", Confidence: ConfidenceConfirmed,
		EvidenceRefs: []string{evidenceID}, Attributes: map[string]any{"configured": true},
	})
	return s
}

func FuzzSnapshotValidationRejectsSensitiveFields(f *testing.F) {
	for _, key := range []string{"secret", "api_key", "access-token", "password", "authorization", "cookie", "prompt", "content", "command_args"} {
		f.Add(key, "private-value")
	}
	f.Fuzz(func(t *testing.T, key, value string) {
		if !IsSensitiveKey(key) {
			return
		}
		snapshot := validSnapshot()
		snapshot.Entities[0].Attributes[key] = value
		if err := snapshot.Validate(); err == nil {
			t.Fatalf("sensitive field %q passed validation", key)
		}
	})
}

func TestSnapshotValidationRejectsSensitiveData(t *testing.T) {
	keys := []string{"api_key", "access_token", "password", "authorization", "command_args", "prompt"}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			s := validSnapshot()
			s.Entities[0].Attributes[key] = "must-not-leave-collector"
			if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "forbidden sensitive field") {
				t.Fatalf("expected sensitive key rejection, got %v", err)
			}
		})
	}
}

func TestSnapshotValidationAcceptsPresenceFacts(t *testing.T) {
	s := validSnapshot()
	s.Entities[0].Attributes["credential_present"] = true
	s.Entities[0].Attributes["environment_key_count"] = 3
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestStableIDsAreOrganizationScoped(t *testing.T) {
	a := StableID("org-a", KindAgent, "repo:example/agent")
	b := StableID("org-a", KindAgent, "repo:example/agent")
	c := StableID("org-b", KindAgent, "repo:example/agent")
	if a != b || a == c {
		t.Fatalf("stable identity invariant failed: %s %s %s", a, b, c)
	}
}
