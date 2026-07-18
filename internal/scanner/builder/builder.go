package builder

import (
	"sort"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

// Builder de-duplicates observations and applies Lens's confidence rules while
// collectors remain focused on gathering evidence.
type Builder struct {
	Snapshot      discovery.Snapshot
	entityIndex   map[string]int
	evidenceIndex map[string]int
	relationIndex map[string]int
}

type Observation struct {
	DetectorID      string
	DetectorVersion string
	Method          string
	Family          string
	Specificity     string
	Locator         string
	ContentHash     string
}

func New(snapshot discovery.Snapshot) *Builder {
	return &Builder{
		Snapshot: snapshot, entityIndex: map[string]int{}, evidenceIndex: map[string]int{}, relationIndex: map[string]int{},
	}
}

func (b *Builder) AddEvidence(observation Observation) string {
	id := discovery.EvidenceID(b.Snapshot.SourceID, observation.DetectorID, observation.Method, observation.Locator, observation.ContentHash)
	if _, exists := b.evidenceIndex[id]; exists {
		return id
	}
	b.evidenceIndex[id] = len(b.Snapshot.Evidence)
	b.Snapshot.Evidence = append(b.Snapshot.Evidence, discovery.Evidence{
		ID: id, DetectorID: observation.DetectorID, DetectorVersion: observation.DetectorVersion,
		Method: observation.Method, Family: observation.Family, Specificity: observation.Specificity,
		Locator: observation.Locator, ContentHash: observation.ContentHash,
		ObservedAt: b.Snapshot.ObservedAt,
	})
	return id
}

func (b *Builder) AddEntity(kind discovery.EntityKind, canonicalKey, name string, attributes map[string]any, refs ...string) string {
	id := discovery.StableID(b.Snapshot.OrganizationID, kind, canonicalKey)
	if index, exists := b.entityIndex[id]; exists {
		entity := &b.Snapshot.Entities[index]
		mergeAttributes(entity.Attributes, attributes)
		entity.EvidenceRefs = union(entity.EvidenceRefs, refs)
		entity.Confidence = b.confidence(entity.EvidenceRefs)
		return id
	}
	if attributes == nil {
		attributes = map[string]any{}
	}
	b.entityIndex[id] = len(b.Snapshot.Entities)
	b.Snapshot.Entities = append(b.Snapshot.Entities, discovery.Entity{
		ID: id, Kind: kind, CanonicalKey: canonicalKey, Name: name, Attributes: attributes,
		Confidence: b.confidence(refs), EvidenceRefs: union(nil, refs),
	})
	return id
}

func (b *Builder) AddRelationship(kind discovery.RelationshipKind, from, to string, attributes map[string]any, refs ...string) string {
	id := discovery.RelationshipID(b.Snapshot.OrganizationID, kind, from, to)
	if index, exists := b.relationIndex[id]; exists {
		relation := &b.Snapshot.Relationships[index]
		mergeAttributes(relation.Attributes, attributes)
		relation.EvidenceRefs = union(relation.EvidenceRefs, refs)
		relation.Confidence = b.confidence(relation.EvidenceRefs)
		return id
	}
	if attributes == nil {
		attributes = map[string]any{}
	}
	b.relationIndex[id] = len(b.Snapshot.Relationships)
	b.Snapshot.Relationships = append(b.Snapshot.Relationships, discovery.Relationship{
		ID: id, Kind: kind, From: from, To: to, Attributes: attributes,
		Confidence: b.confidence(refs), EvidenceRefs: union(nil, refs),
	})
	return id
}

func (b *Builder) Error(detectorID, code, message string, retryable bool) {
	b.Snapshot.Errors = append(b.Snapshot.Errors, discovery.ScanError{
		DetectorID: detectorID, Code: code, Message: message, Retryable: retryable,
	})
	b.Snapshot.Coverage.DetectorsFailed++
	b.Snapshot.Coverage.Partial = true
}

func (b *Builder) Finish() (discovery.Snapshot, error) {
	b.Snapshot.Normalize()
	return b.Snapshot, b.Snapshot.Validate()
}

func (b *Builder) confidence(refs []string) discovery.Confidence {
	families := map[string]struct{}{}
	authoritative := false
	high := 0
	for _, ref := range refs {
		index, ok := b.evidenceIndex[ref]
		if !ok {
			continue
		}
		evidence := b.Snapshot.Evidence[index]
		if evidence.Method == "descriptor" || evidence.Method == "api_document" || evidence.Method == "workload_uid" {
			authoritative = true
		}
		if evidence.Specificity == "high" {
			if _, seen := families[evidence.Family]; !seen {
				high++
				families[evidence.Family] = struct{}{}
			}
		}
	}
	if authoritative || high >= 2 {
		return discovery.ConfidenceConfirmed
	}
	if high == 1 {
		return discovery.ConfidenceLikely
	}
	return discovery.ConfidencePossible
}

func mergeAttributes(target, incoming map[string]any) {
	for key, value := range incoming {
		if current, exists := target[key]; exists {
			currentBool, currentOK := current.(bool)
			incomingBool, incomingOK := value.(bool)
			if currentOK && incomingOK {
				target[key] = currentBool || incomingBool
			}
			continue
		}
		target[key] = value
	}
}

func union(existing []string, additions []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(additions))
	for _, item := range append(existing, additions...) {
		if strings.TrimSpace(item) != "" {
			seen[item] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for item := range seen {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}
