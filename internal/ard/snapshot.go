package ard

import (
	"fmt"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type SnapshotOptions struct {
	OrganizationID string
	SourceID       string
	TargetID       string
	SourceType     discovery.SourceType
	SourceName     string
	SourceLocator  string
	ContentHash    string
	SourceSurface  string
	Collector      discovery.Collector
}

func Snapshot(result Result, options SnapshotOptions) (discovery.Snapshot, error) {
	if options.OrganizationID == "" || options.SourceID == "" || options.TargetID == "" {
		return discovery.Snapshot{}, fmt.Errorf("organization, source, and target IDs are required")
	}
	if options.SourceType == "" {
		options.SourceType = discovery.SourceCatalog
	}
	if options.SourceSurface == "" {
		options.SourceSurface = string(options.SourceType)
	}
	if options.SourceName == "" {
		options.SourceName = result.Catalog.Host.DisplayName
	}
	if options.SourceName == "" {
		options.SourceName = "ARD Catalog"
	}
	if options.Collector.ID == "" {
		options.Collector = discovery.Collector{ID: "lens-ard", Name: "Barrikade Lens ARD", Version: "1", Mode: options.SourceSurface}
	}
	snapshot := discovery.NewTargetSnapshot(options.OrganizationID, options.SourceID, options.TargetID, options.SourceType, options.Collector)
	snapshot.Scope = discovery.Scope{Name: options.SourceName}
	b := builder.New(snapshot)
	ref := b.AddEvidence(builder.Observation{
		DetectorID: "ard.catalog", DetectorVersion: "1", Method: "descriptor", Family: "publisher_declaration",
		Specificity: "high", Locator: options.SourceLocator, ContentHash: options.ContentHash, Authoritative: true,
	})
	catalogCanonical := "ard:catalog:" + options.SourceLocator
	catalogAttrs := map[string]any{"ard_spec_version": result.Catalog.SpecVersion, "source_surface": options.SourceSurface, "defined": true}
	if result.Catalog.Host.Identifier != "" {
		catalogAttrs["host_identifier"] = result.Catalog.Host.Identifier
	}
	catalogID := b.AddEntity(discovery.KindCatalog, catalogCanonical, options.SourceName, catalogAttrs, ref)
	if AddToBuilder(b, catalogID, "", result.Catalog, ref, options.SourceSurface) {
		b.Snapshot.Full = false
		b.Snapshot.Coverage.Partial = true
		b.Snapshot.Coverage.Notes = append(b.Snapshot.Coverage.Notes, "ARD declaration traversal reached its 25,000 entry safety limit")
	}
	for _, warning := range result.Warnings {
		b.Error("ard.catalog", "invalid_catalog_entry", warning, false)
	}
	b.Snapshot.Coverage.DetectorsRun = 1
	b.Snapshot.Coverage.LocationsChecked = 1
	return b.Finish()
}

// AddToBuilder adds a parsed catalog to an existing discovery snapshot. A
// repositoryID associates every declaration with the repository that defined
// it; catalog-source snapshots leave it empty.
func AddToBuilder(b *builder.Builder, catalogID, repositoryID string, catalog Catalog, ref, surface string) bool {
	count := 0
	truncated := false
	addEntries(b, catalogID, repositoryID, catalog, ref, surface, map[string]bool{}, 0, &count, &truncated)
	return truncated
}

func addEntries(b *builder.Builder, catalogID, repositoryID string, catalog Catalog, ref, surface string, visited map[string]bool, depth int, count *int, truncated *bool) {
	if depth > MaxDepth {
		*truncated = true
		return
	}
	for index, entry := range catalog.Entries {
		if *count >= MaxEntriesPerSource {
			if index < len(catalog.Entries) {
				*truncated = true
			}
			return
		}
		attrs := map[string]any{
			"ard_identifier": entry.Identifier, "publisher_domain": entry.PublisherDomain,
			"media_type": entry.MediaType, "mapped_kind": entry.MappedKind, "delivery": entry.Delivery,
			"source_surface": surface, "defined": true,
			"trust_identity_alignment": entry.TrustIdentityAlignment, "signature_status": entry.SignatureStatus,
		}
		for key, value := range map[string]string{
			"description": entry.Description, "version": entry.Version, "updated_at": entry.UpdatedAt,
			"artifact_url": entry.ArtifactURL, "host": discovery.URLHost(entry.ArtifactURL),
			"artifact_fingerprint": entry.ArtifactFingerprint, "protocol_identity": entry.ProtocolIdentity,
			"repository_url": entry.RepositoryURL, "repository_path": entry.RepositoryPath,
			"trust_identity_type": entry.TrustIdentityType,
			"signature_digest":    entry.SignatureDigest,
		} {
			if strings.TrimSpace(value) != "" {
				attrs[key] = value
			}
		}
		if len(entry.Tags) > 0 {
			attrs["tags"] = entry.Tags
		}
		if len(entry.Capabilities) > 0 {
			attrs["declared_capabilities"] = entry.Capabilities
		}
		if len(entry.AttestationTypes) > 0 {
			attrs["claimed_attestation_types"] = entry.AttestationTypes
		}
		declarationID := b.AddEntity(discovery.KindResourceDeclaration, "ard:"+entry.Identifier, entry.DisplayName, attrs, ref)
		(*count)++
		b.AddRelationship(discovery.RelationshipPublishes, catalogID, declarationID, map[string]any{"source_surface": surface}, ref)
		if repositoryID != "" {
			b.AddRelationship(discovery.RelationshipDefinedIn, declarationID, repositoryID, map[string]any{"source_surface": surface}, ref)
		}
		if entry.InlineCatalog == nil || visited[entry.Identifier] {
			continue
		}
		visited[entry.Identifier] = true
		nestedID := b.AddEntity(discovery.KindCatalog, "ard:inline-catalog:"+entry.Identifier, entry.DisplayName, map[string]any{
			"ard_spec_version": entry.InlineCatalog.SpecVersion, "source_surface": surface, "defined": true, "inline": true,
		}, ref)
		b.AddRelationship(discovery.RelationshipReferences, catalogID, nestedID, map[string]any{"delivery": "inline"}, ref)
		addEntries(b, nestedID, repositoryID, *entry.InlineCatalog, ref, surface, visited, depth+1, count, truncated)
	}
}
