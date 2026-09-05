package cloud

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type CredentialBroker interface {
	Acquire(context.Context, Environment) (Credentials, error)
}

type Detector interface {
	ID() string
	Version() string
	Preview() bool
	Detect(context.Context, Environment, Credentials) ([]Resource, error)
}

type Resource struct {
	ProviderID      string
	Kind            discovery.EntityKind
	Name            string
	Location        string
	Product         string
	ResourceType    string
	NetworkScope    string
	Endpoint        string
	LinkedIdentity  string
	KnowledgeStores []LinkedResource
	References      []LinkedResource
	Attributes      map[string]any
}

type LinkedResource struct {
	ProviderID string
	Name       string
	Kind       discovery.EntityKind
	Relation   discovery.RelationshipKind
	Attributes map[string]any
}

type ManagedAdapter struct {
	Provider  string
	Broker    CredentialBroker
	Detectors []Detector
}

func (a *ManagedAdapter) AcquireTemporaryCredentials(ctx context.Context, environment Environment) (Credentials, error) {
	if a.Broker == nil {
		return Credentials{}, &Error{Code: "connector_unavailable", Message: "The provider credential broker is not configured"}
	}
	credentials, err := a.Broker.Acquire(ctx, environment)
	if err != nil {
		return Credentials{}, err
	}
	if credentials.ExpiresAt.IsZero() || time.Until(credentials.ExpiresAt) <= 0 {
		return Credentials{}, &Error{Code: "authentication_failed", Message: "The provider returned expired credentials"}
	}
	return credentials, nil
}

func (a *ManagedAdapter) Verify(ctx context.Context, environment Environment) (Verification, error) {
	credentials, err := a.AcquireTemporaryCredentials(ctx, environment)
	if err != nil {
		return Verification{}, err
	}
	if len(a.Detectors) == 0 {
		return Verification{}, &Error{Code: "connector_unavailable", Message: "No provider detectors are configured"}
	}
	for _, detector := range a.Detectors {
		_, err := detector.Detect(ctx, environment, credentials)
		if err == nil {
			permissions := make([]string, 0, len(a.Detectors))
			for _, item := range a.Detectors {
				permissions = append(permissions, item.ID())
			}
			return Verification{Principal: a.Provider + ":federated-workload", Permissions: permissions, Metadata: map[string]string{"credential_expiry": credentials.ExpiresAt.UTC().Format(time.RFC3339)}}, nil
		}
		code, _, retryable, _ := SafeError(err)
		if code == "permission_denied" || code == "authentication_failed" || retryable {
			return Verification{}, err
		}
		// A preview detector being unavailable does not make the stable provider
		// connection unverifiable; continue until one detector responds.
	}
	return Verification{}, &Error{Code: "permission_denied", Message: "Lens could not read any supported AI resource APIs"}
}

func (a *ManagedAdapter) Scan(ctx context.Context, environment Environment, sequence uint64, progress ProgressFunc) (discovery.Snapshot, error) {
	credentials, err := a.AcquireTemporaryCredentials(ctx, environment)
	if err != nil {
		return discovery.Snapshot{}, err
	}
	snapshot := discovery.NewTargetSnapshot(environment.OrganizationID, environment.SourceID, environment.TargetID, discovery.SourceCloud, discovery.Collector{ID: "lens-cloud-" + a.Provider, Name: "Lens " + strings.ToUpper(a.Provider) + " adapter", Version: "1.0.0", Mode: "managed"})
	snapshot.Sequence = sequence
	snapshot.Scope = discovery.Scope{Name: environment.DisplayName, Attributes: map[string]string{"platform": a.Provider, "provider_scope": environment.ExternalID}}
	b := builder.New(snapshot)
	environmentEvidence := b.AddEvidence(builder.Observation{DetectorID: a.Provider + ".environment", DetectorVersion: "1.0.0", Method: "provider-api", Family: "cloud-control-plane", Specificity: "high", Locator: environment.ExternalID, Authoritative: true})
	cloudEnvironmentID := b.AddEntity(discovery.KindCloudEnvironment, a.Provider+":"+environment.ExternalID, environment.DisplayName, map[string]any{"source_surface": "cloud", "provider": a.Provider, "external_id": environment.ExternalID, "system_role": "target", "discovery_state": "deployed", "network_scope": "none"}, environmentEvidence)
	for index, detector := range a.Detectors {
		if progress != nil {
			progress(Progress{Phase: "scanning:" + detector.ID(), Completed: index, Total: len(a.Detectors), Attributes: map[string]any{"preview": detector.Preview()}})
		}
		resources, detectErr := detector.Detect(ctx, environment, credentials)
		b.Snapshot.Coverage.DetectorsRun++
		for _, resource := range resources {
			a.addResource(b, cloudEnvironmentID, environment, detector, resource)
		}
		if detectErr != nil {
			code, message, retryable, _ := SafeError(detectErr)
			b.Error(detector.ID(), code, message, retryable)
			if code == "permission_denied" {
				b.Snapshot.Coverage.LocationsDenied++
			}
			continue
		}
		b.Snapshot.Coverage.LocationsChecked++
		if detector.Preview() {
			b.Snapshot.Coverage.Notes = append(b.Snapshot.Coverage.Notes, detector.ID()+" uses a version-pinned preview API")
		}
	}
	if progress != nil {
		progress(Progress{Phase: "building_snapshot", Completed: len(a.Detectors), Total: len(a.Detectors), Attributes: map[string]any{"entities": len(b.Snapshot.Entities)}})
	}
	return b.Finish()
}

func (a *ManagedAdapter) addResource(b *builder.Builder, environmentID string, environment Environment, detector Detector, resource Resource) {
	if resource.ProviderID == "" || resource.Name == "" {
		return
	}
	if resource.Kind == "" {
		resource.Kind = discovery.KindRuntime
	}
	attributes := map[string]any{
		"source_surface": "cloud", "provider": a.Provider, "provider_resource_id": resource.ProviderID,
		"location": resource.Location, "product": resource.Product, "resource_type": resource.ResourceType,
		"network_scope": normalizeNetworkScope(resource.NetworkScope, resource.Endpoint), "discovery_state": "deployed",
		"preview": detector.Preview(),
	}
	for key, value := range resource.Attributes {
		attributes[key] = value
	}
	evidence := b.AddEvidence(builder.Observation{DetectorID: detector.ID(), DetectorVersion: detector.Version(), Method: "provider-api", Family: a.Provider + "-control-plane", Specificity: "high", Locator: resource.ProviderID, Authoritative: true})
	resourceID := b.AddEntity(resource.Kind, a.Provider+":"+resource.ProviderID, resource.Name, attributes, evidence)
	b.AddRelationship(discovery.RelationshipContainedIn, resourceID, environmentID, map[string]any{"provider": a.Provider}, evidence)
	if resource.LinkedIdentity != "" {
		identityID := b.AddEntity(discovery.KindIdentity, a.Provider+":"+resource.LinkedIdentity, resource.LinkedIdentity, map[string]any{"source_surface": "cloud", "provider": a.Provider, "discovery_state": "configured", "network_scope": "none"}, evidence)
		b.AddRelationship(discovery.RelationshipRunsAs, resourceID, identityID, nil, evidence)
	}
	for _, linked := range append(resource.KnowledgeStores, resource.References...) {
		if linked.ProviderID == "" {
			continue
		}
		kind := linked.Kind
		if kind == "" {
			kind = discovery.KindKnowledgeStore
		}
		name := linked.Name
		if name == "" {
			name = linked.ProviderID
		}
		linkedAttributes := map[string]any{"source_surface": "cloud", "provider": a.Provider, "discovery_state": "configured", "network_scope": "none"}
		for key, value := range linked.Attributes {
			linkedAttributes[key] = value
		}
		linkedID := b.AddEntity(kind, a.Provider+":"+linked.ProviderID, name, linkedAttributes, evidence)
		relation := linked.Relation
		if relation == "" {
			relation = discovery.RelationshipUses
		}
		b.AddRelationship(relation, resourceID, linkedID, nil, evidence)
	}
}

func normalizeNetworkScope(value, endpoint string) string {
	switch value {
	case "loopback", "network", "external", "none", "unknown":
		return value
	}
	if endpoint != "" {
		return "external"
	}
	return "unknown"
}

func (a *ManagedAdapter) String() string {
	return fmt.Sprintf("managed cloud adapter %s (%d detectors)", a.Provider, len(a.Detectors))
}
