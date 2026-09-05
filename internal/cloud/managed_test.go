package cloud

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type fixtureDetector struct {
	id        string
	preview   bool
	resources []Resource
	err       error
}

func (d fixtureDetector) ID() string    { return d.id }
func (fixtureDetector) Version() string { return "fixture-v1" }
func (d fixtureDetector) Preview() bool { return d.preview }
func (d fixtureDetector) Detect(context.Context, Environment, Credentials) ([]Resource, error) {
	return d.resources, d.err
}

func TestManagedAdapterPreservesUsefulResultsWhenDetectorFails(t *testing.T) {
	adapter := &ManagedAdapter{Provider: "gcp", Broker: StaticBroker{Credentials: Credentials{BearerToken: "temporary", ExpiresAt: time.Now().Add(time.Hour)}}, Detectors: []Detector{
		fixtureDetector{id: "stable", resources: []Resource{{ProviderID: "projects/p/locations/us/reasoningEngines/a", Kind: discovery.KindAgent, Name: "Agent", LinkedIdentity: "serviceAccount:agent", KnowledgeStores: []LinkedResource{{ProviderID: "rag/one", Name: "Knowledge", Kind: discovery.KindKnowledgeStore}}}}},
		fixtureDetector{id: "preview", preview: true, err: &Error{Code: "api_unavailable", Message: "Preview API is not enabled"}},
	}}
	environment := Environment{ID: "environment", OrganizationID: "org", Provider: "gcp", ExternalID: "project", DisplayName: "Project", SourceID: "source", TargetID: "target"}
	snapshot, err := adapter.Scan(t.Context(), environment, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Coverage.Partial || snapshot.Coverage.DetectorsRun != 2 || snapshot.Coverage.DetectorsFailed != 1 || len(snapshot.Errors) != 1 {
		t.Fatalf("unexpected partial coverage: %+v errors=%+v", snapshot.Coverage, snapshot.Errors)
	}
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	if kinds[discovery.KindAgent] != 1 || kinds[discovery.KindIdentity] != 1 || kinds[discovery.KindKnowledgeStore] != 1 || kinds[discovery.KindCloudEnvironment] != 1 {
		t.Fatalf("successful detector results were not preserved: %#v", kinds)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedAdapterRejectsExpiredCredentials(t *testing.T) {
	adapter := &ManagedAdapter{Provider: "aws", Broker: StaticBroker{Credentials: Credentials{AccessKeyID: "a", SecretAccessKey: "b", ExpiresAt: time.Now().Add(-time.Second)}}, Detectors: []Detector{fixtureDetector{id: "stable"}}}
	_, err := adapter.Scan(t.Context(), Environment{}, 0, nil)
	var safe *Error
	if !errors.As(err, &safe) || safe.Code != "authentication_failed" {
		t.Fatalf("expected authentication failure, got %v", err)
	}
}
