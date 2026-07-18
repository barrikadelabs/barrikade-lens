package discovery

import (
	"fmt"
	"sort"
)

// MergeSnapshots combines independently collected surfaces into one export
// envelope without losing evidence attached to duplicate stable identities.
func MergeSnapshots(target *Snapshot, addition Snapshot) error {
	if target.OrganizationID != addition.OrganizationID {
		return fmt.Errorf("cannot merge snapshots from different organizations")
	}
	evidenceSeen := make(map[string]struct{}, len(target.Evidence))
	for _, item := range target.Evidence {
		evidenceSeen[item.ID] = struct{}{}
	}
	for _, item := range addition.Evidence {
		if _, exists := evidenceSeen[item.ID]; !exists {
			target.Evidence = append(target.Evidence, item)
			evidenceSeen[item.ID] = struct{}{}
		}
	}

	entityIndex := make(map[string]int, len(target.Entities))
	for index, item := range target.Entities {
		entityIndex[item.ID] = index
	}
	for _, item := range addition.Entities {
		if index, exists := entityIndex[item.ID]; exists {
			current := &target.Entities[index]
			current.Attributes = mergeMaps(current.Attributes, item.Attributes)
			current.EvidenceRefs = mergeStrings(current.EvidenceRefs, item.EvidenceRefs)
			current.Provenance = mergeStrings(current.Provenance, item.Provenance)
			current.Confidence = strongerConfidence(current.Confidence, item.Confidence)
			continue
		}
		entityIndex[item.ID] = len(target.Entities)
		target.Entities = append(target.Entities, item)
	}

	relationshipIndex := make(map[string]int, len(target.Relationships))
	for index, item := range target.Relationships {
		relationshipIndex[item.ID] = index
	}
	for _, item := range addition.Relationships {
		if index, exists := relationshipIndex[item.ID]; exists {
			current := &target.Relationships[index]
			current.Attributes = mergeMaps(current.Attributes, item.Attributes)
			current.EvidenceRefs = mergeStrings(current.EvidenceRefs, item.EvidenceRefs)
			current.Confidence = strongerConfidence(current.Confidence, item.Confidence)
			continue
		}
		relationshipIndex[item.ID] = len(target.Relationships)
		target.Relationships = append(target.Relationships, item)
	}

	target.Coverage.DetectorsRun += addition.Coverage.DetectorsRun
	target.Coverage.DetectorsFailed += addition.Coverage.DetectorsFailed
	target.Coverage.LocationsChecked += addition.Coverage.LocationsChecked
	target.Coverage.LocationsDenied += addition.Coverage.LocationsDenied
	target.Coverage.Partial = target.Coverage.Partial || addition.Coverage.Partial
	target.Coverage.Notes = mergeStrings(target.Coverage.Notes, addition.Coverage.Notes)
	target.Errors = append(target.Errors, addition.Errors...)
	target.Normalize()
	return nil
}

func mergeMaps(current, incoming map[string]any) map[string]any {
	if current == nil {
		current = map[string]any{}
	}
	for key, value := range incoming {
		existing, exists := current[key]
		if !exists {
			current[key] = value
			continue
		}
		existingBool, existingOK := existing.(bool)
		incomingBool, incomingOK := value.(bool)
		if existingOK && incomingOK {
			current[key] = existingBool || incomingBool
		}
	}
	return current
}

func mergeStrings(current, incoming []string) []string {
	seen := make(map[string]struct{}, len(current)+len(incoming))
	for _, item := range append(current, incoming...) {
		if item != "" {
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

func strongerConfidence(left, right Confidence) Confidence {
	rank := map[Confidence]int{ConfidencePossible: 1, ConfidenceLikely: 2, ConfidenceConfirmed: 3}
	if rank[right] > rank[left] {
		return right
	}
	return left
}
