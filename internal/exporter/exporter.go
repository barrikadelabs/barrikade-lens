package exporter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type Format string

const (
	FormatHuman     Format = "human"
	FormatJSON      Format = "json"
	FormatNDJSON    Format = "ndjson"
	FormatCycloneDX Format = "cyclonedx"
)

func Write(writer io.Writer, snapshot discovery.Snapshot, format Format) error {
	switch format {
	case FormatHuman:
		return writeHuman(writer, snapshot)
	case FormatJSON:
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(snapshot)
	case FormatNDJSON:
		return writeNDJSON(writer, snapshot)
	case FormatCycloneDX:
		return writeCycloneDX(writer, snapshot)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func ValidFormat(value string) bool {
	switch Format(value) {
	case FormatHuman, FormatJSON, FormatNDJSON, FormatCycloneDX:
		return true
	}
	return false
}

func writeHuman(writer io.Writer, snapshot discovery.Snapshot) error {
	kinds := map[discovery.EntityKind]int{}
	for _, entity := range snapshot.Entities {
		kinds[entity.Kind]++
	}
	orderedKinds := make([]string, 0, len(kinds))
	for kind := range kinds {
		orderedKinds = append(orderedKinds, string(kind))
	}
	sort.Strings(orderedKinds)
	status := "complete"
	if snapshot.Coverage.Partial {
		status = "partial"
	}
	if _, err := fmt.Fprintf(writer, "Barrikade Lens discovery %s\nSource: %s (%s)\nCoverage: %s · %d detectors · %d locations checked\n\n", snapshot.SnapshotID, snapshot.Scope.Name, snapshot.SourceType, status, snapshot.Coverage.DetectorsRun, snapshot.Coverage.LocationsChecked); err != nil {
		return err
	}
	if len(orderedKinds) == 0 {
		_, err := fmt.Fprintln(writer, "No agent-related entities were discovered.")
		return err
	}
	if _, err := fmt.Fprintln(writer, "Inventory"); err != nil {
		return err
	}
	for _, kind := range orderedKinds {
		if _, err := fmt.Fprintf(writer, "  %-24s %d\n", strings.ReplaceAll(kind, "_", " "), kinds[discovery.EntityKind(kind)]); err != nil {
			return err
		}
	}
	if len(snapshot.Errors) > 0 {
		if _, err := fmt.Fprintln(writer, "\nCoverage notes"); err != nil {
			return err
		}
		for _, scanError := range snapshot.Errors {
			if _, err := fmt.Fprintf(writer, "  %s: %s\n", scanError.Code, scanError.Message); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeNDJSON(writer io.Writer, snapshot discovery.Snapshot) error {
	buffer := bufio.NewWriter(writer)
	defer buffer.Flush()
	encoder := json.NewEncoder(buffer)
	metadata := map[string]any{
		"schema_version": snapshot.SchemaVersion, "snapshot_id": snapshot.SnapshotID,
		"organization_id": snapshot.OrganizationID, "source_id": snapshot.SourceID,
		"source_type": snapshot.SourceType, "observed_at": snapshot.ObservedAt,
	}
	if err := encoder.Encode(map[string]any{"record_type": "snapshot", "metadata": metadata, "coverage": snapshot.Coverage}); err != nil {
		return err
	}
	for _, entity := range snapshot.Entities {
		if err := encoder.Encode(map[string]any{"record_type": "entity", "metadata": metadata, "entity": entity}); err != nil {
			return err
		}
	}
	for _, relationship := range snapshot.Relationships {
		if err := encoder.Encode(map[string]any{"record_type": "relationship", "metadata": metadata, "relationship": relationship}); err != nil {
			return err
		}
	}
	return nil
}

type cycloneDXBOM struct {
	BOMFormat    string                `json:"bomFormat"`
	SpecVersion  string                `json:"specVersion"`
	Serial       string                `json:"serialNumber"`
	Version      int                   `json:"version"`
	Metadata     cycloneDXMetadata     `json:"metadata"`
	Components   []cycloneDXComponent  `json:"components,omitempty"`
	Services     []cycloneDXService    `json:"services,omitempty"`
	Dependencies []cycloneDXDependency `json:"dependencies,omitempty"`
}

type cycloneDXMetadata struct {
	Timestamp string             `json:"timestamp"`
	Component cycloneDXComponent `json:"component"`
}
type cycloneDXProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type cycloneDXComponent struct {
	Type       string              `json:"type"`
	BOMRef     string              `json:"bom-ref"`
	Name       string              `json:"name"`
	Properties []cycloneDXProperty `json:"properties,omitempty"`
}
type cycloneDXService struct {
	BOMRef     string              `json:"bom-ref"`
	Name       string              `json:"name"`
	Endpoints  []string            `json:"endpoints,omitempty"`
	Properties []cycloneDXProperty `json:"properties,omitempty"`
}
type cycloneDXDependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

func writeCycloneDX(writer io.Writer, snapshot discovery.Snapshot) error {
	bom := cycloneDXBOM{
		BOMFormat: "CycloneDX", SpecVersion: "1.7", Serial: "urn:uuid:" + snapshot.SnapshotID, Version: 1,
		Metadata: cycloneDXMetadata{Timestamp: snapshot.ObservedAt, Component: cycloneDXComponent{Type: "application", BOMRef: snapshot.SourceID, Name: snapshot.Scope.Name}},
	}
	componentIDs := map[string]bool{}
	serviceIDs := map[string]bool{}
	for _, entity := range snapshot.Entities {
		properties := []cycloneDXProperty{{Name: "barrikade:lens:kind", Value: string(entity.Kind)}, {Name: "barrikade:lens:confidence", Value: string(entity.Confidence)}}
		for _, ref := range entity.EvidenceRefs {
			properties = append(properties, cycloneDXProperty{Name: "barrikade:lens:evidence", Value: ref})
		}
		if entity.Kind == discovery.KindAPIService {
			service := cycloneDXService{BOMRef: entity.ID, Name: entity.Name, Properties: properties}
			if servers, ok := entity.Attributes["servers"].([]string); ok {
				service.Endpoints = servers
			}
			if endpoint, ok := entity.Attributes["endpoint"].(string); ok {
				service.Endpoints = []string{endpoint}
			}
			bom.Services = append(bom.Services, service)
			serviceIDs[entity.ID] = true
			continue
		}
		typeName := "application"
		switch entity.Kind {
		case discovery.KindFramework:
			typeName = "library"
		case discovery.KindModel:
			typeName = "machine-learning-model"
		case discovery.KindEndpoint, discovery.KindCluster:
			typeName = "device"
		case discovery.KindRepository:
			typeName = "data"
		}
		bom.Components = append(bom.Components, cycloneDXComponent{Type: typeName, BOMRef: entity.ID, Name: entity.Name, Properties: properties})
		componentIDs[entity.ID] = true
	}
	dependencies := map[string][]string{}
	for _, relationship := range snapshot.Relationships {
		if (componentIDs[relationship.From] || serviceIDs[relationship.From]) && (componentIDs[relationship.To] || serviceIDs[relationship.To]) {
			dependencies[relationship.From] = append(dependencies[relationship.From], relationship.To)
		}
	}
	for from, to := range dependencies {
		sort.Strings(to)
		bom.Dependencies = append(bom.Dependencies, cycloneDXDependency{Ref: from, DependsOn: to})
	}
	sort.Slice(bom.Dependencies, func(i, j int) bool { return bom.Dependencies[i].Ref < bom.Dependencies[j].Ref })
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(bom)
}
