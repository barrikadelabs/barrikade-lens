// Package discovery defines the stable, language-neutral Lens discovery
// contract shared by collectors, Hub, exporters, and control-plane consumers.
package discovery

import (
	"time"

	"github.com/google/uuid"
)

const SchemaVersion = "1.0"

type SourceType string

const (
	SourceEndpoint   SourceType = "endpoint"
	SourceRepository SourceType = "repository"
	SourceKubernetes SourceType = "kubernetes"
)

type EntityKind string

const (
	KindEndpoint            EntityKind = "endpoint"
	KindUser                EntityKind = "user"
	KindRepository          EntityKind = "repository"
	KindCluster             EntityKind = "cluster"
	KindWorkload            EntityKind = "workload"
	KindAgent               EntityKind = "agent"
	KindRuntime             EntityKind = "runtime"
	KindFramework           EntityKind = "framework"
	KindMCPServer           EntityKind = "mcp_server"
	KindSkill               EntityKind = "skill"
	KindModel               EntityKind = "model"
	KindModelServer         EntityKind = "model_server"
	KindTool                EntityKind = "tool"
	KindAPIService          EntityKind = "api_service"
	KindAPIOperation        EntityKind = "api_operation"
	KindWorkflow            EntityKind = "workflow"
	KindCredentialReference EntityKind = "credential_reference"
)

type RelationshipKind string

const (
	RelationshipRunsOn       RelationshipKind = "runs_on"
	RelationshipDefinedIn    RelationshipKind = "defined_in"
	RelationshipDeployedAs   RelationshipKind = "deployed_as"
	RelationshipUses         RelationshipKind = "uses"
	RelationshipExposes      RelationshipKind = "exposes"
	RelationshipConnectsTo   RelationshipKind = "connects_to"
	RelationshipProvides     RelationshipKind = "provides"
	RelationshipInvokes      RelationshipKind = "invokes"
	RelationshipConfiguredBy RelationshipKind = "configured_by"
	RelationshipOwnedBy      RelationshipKind = "owned_by"
)

type Confidence string

const (
	ConfidenceConfirmed Confidence = "confirmed"
	ConfidenceLikely    Confidence = "likely"
	ConfidencePossible  Confidence = "possible"
)

type Collector struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Mode    string `json:"mode"`
}

type Scope struct {
	Name       string            `json:"name,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type Coverage struct {
	DetectorsRun     int      `json:"detectors_run"`
	DetectorsFailed  int      `json:"detectors_failed"`
	LocationsChecked int      `json:"locations_checked"`
	LocationsDenied  int      `json:"locations_denied"`
	Partial          bool     `json:"partial"`
	Notes            []string `json:"notes,omitempty"`
}

type ScanError struct {
	DetectorID string `json:"detector_id,omitempty"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
}

type Entity struct {
	ID           string         `json:"id"`
	Kind         EntityKind     `json:"kind"`
	CanonicalKey string         `json:"canonical_key,omitempty"`
	Name         string         `json:"name"`
	Attributes   map[string]any `json:"attributes,omitempty"`
	Confidence   Confidence     `json:"confidence"`
	EvidenceRefs []string       `json:"evidence_refs,omitempty"`
	Provenance   []string       `json:"provenance,omitempty"`
}

type Relationship struct {
	ID           string           `json:"id"`
	Kind         RelationshipKind `json:"kind"`
	From         string           `json:"from"`
	To           string           `json:"to"`
	Attributes   map[string]any   `json:"attributes,omitempty"`
	Confidence   Confidence       `json:"confidence"`
	EvidenceRefs []string         `json:"evidence_refs,omitempty"`
}

type Evidence struct {
	ID              string `json:"id"`
	DetectorID      string `json:"detector_id"`
	DetectorVersion string `json:"detector_version"`
	Method          string `json:"method"`
	Family          string `json:"family"`
	Specificity     string `json:"specificity"`
	Locator         string `json:"locator,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
	ObservedAt      string `json:"observed_at"`
}

type Snapshot struct {
	SchemaVersion  string         `json:"schema_version"`
	SnapshotID     string         `json:"snapshot_id"`
	OrganizationID string         `json:"organization_id"`
	SourceID       string         `json:"source_id"`
	SourceType     SourceType     `json:"source_type"`
	Collector      Collector      `json:"collector"`
	ObservedAt     string         `json:"observed_at"`
	Full           bool           `json:"full"`
	Sequence       uint64         `json:"sequence"`
	Scope          Scope          `json:"scope"`
	Entities       []Entity       `json:"entities"`
	Relationships  []Relationship `json:"relationships"`
	Evidence       []Evidence     `json:"evidence"`
	Coverage       Coverage       `json:"coverage"`
	Errors         []ScanError    `json:"errors,omitempty"`
}

func NewSnapshot(orgID, sourceID string, sourceType SourceType, collector Collector) Snapshot {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}
	return Snapshot{
		SchemaVersion:  SchemaVersion,
		SnapshotID:     id.String(),
		OrganizationID: orgID,
		SourceID:       sourceID,
		SourceType:     sourceType,
		Collector:      collector,
		ObservedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		Full:           true,
		Entities:       []Entity{},
		Relationships:  []Relationship{},
		Evidence:       []Evidence{},
	}
}
