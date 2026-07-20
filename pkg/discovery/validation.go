package discovery

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Snapshot) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q", SchemaVersion)
	}
	if s.SnapshotID == "" || s.OrganizationID == "" || s.SourceID == "" || s.TargetID == "" {
		return fmt.Errorf("snapshot_id, organization_id, source_id, and target_id are required")
	}
	if s.SourceType != SourceEndpoint && s.SourceType != SourceRepository && s.SourceType != SourceKubernetes {
		return fmt.Errorf("unsupported source_type %q", s.SourceType)
	}
	if _, err := time.Parse(time.RFC3339Nano, s.ObservedAt); err != nil {
		return fmt.Errorf("observed_at: %w", err)
	}
	entities := make(map[string]struct{}, len(s.Entities))
	evidence := make(map[string]struct{}, len(s.Evidence))
	for _, item := range s.Evidence {
		if item.ID == "" || item.DetectorID == "" || item.Method == "" || item.Family == "" {
			return fmt.Errorf("evidence id, detector_id, method, and family are required")
		}
		evidence[item.ID] = struct{}{}
	}
	for _, entity := range s.Entities {
		if entity.ID == "" || entity.Kind == "" || entity.Name == "" {
			return fmt.Errorf("entity id, kind, and name are required")
		}
		if _, exists := entities[entity.ID]; exists {
			return fmt.Errorf("duplicate entity id %q", entity.ID)
		}
		entities[entity.ID] = struct{}{}
		if err := validateConfidence(entity.Confidence); err != nil {
			return fmt.Errorf("entity %s: %w", entity.ID, err)
		}
		if err := validateAttributes(entity.Attributes, "attributes"); err != nil {
			return fmt.Errorf("entity %s: %w", entity.ID, err)
		}
		for _, ref := range entity.EvidenceRefs {
			if _, ok := evidence[ref]; !ok {
				return fmt.Errorf("entity %s references missing evidence %s", entity.ID, ref)
			}
		}
	}
	for _, relationship := range s.Relationships {
		if relationship.ID == "" || relationship.Kind == "" {
			return fmt.Errorf("relationship id and kind are required")
		}
		if _, ok := entities[relationship.From]; !ok {
			return fmt.Errorf("relationship %s has missing from entity", relationship.ID)
		}
		if _, ok := entities[relationship.To]; !ok {
			return fmt.Errorf("relationship %s has missing to entity", relationship.ID)
		}
		if err := validateAttributes(relationship.Attributes, "attributes"); err != nil {
			return fmt.Errorf("relationship %s: %w", relationship.ID, err)
		}
		if err := validateConfidence(relationship.Confidence); err != nil {
			return fmt.Errorf("relationship %s: %w", relationship.ID, err)
		}
		for _, ref := range relationship.EvidenceRefs {
			if _, ok := evidence[ref]; !ok {
				return fmt.Errorf("relationship %s references missing evidence %s", relationship.ID, ref)
			}
		}
	}
	return nil
}

func validateConfidence(value Confidence) error {
	if value != ConfidenceConfirmed && value != ConfidenceLikely && value != ConfidencePossible {
		return fmt.Errorf("invalid confidence %q", value)
	}
	return nil
}

func validateAttributes(value any, path string) error {
	switch typed := value.(type) {
	case nil, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return nil
	case string:
		if len(typed) > 16*1024 {
			return fmt.Errorf("%s exceeds maximum string size", path)
		}
		return nil
	case []string:
		for i, item := range typed {
			if err := validateAttributes(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for i, item := range typed {
			if err := validateAttributes(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case map[string]string:
		for key, item := range typed {
			if IsSensitiveKey(key) {
				return fmt.Errorf("%s.%s is a forbidden sensitive field", path, key)
			}
			if err := validateAttributes(item, path+"."+key); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for key, item := range typed {
			if IsSensitiveKey(key) {
				return fmt.Errorf("%s.%s is a forbidden sensitive field", path, key)
			}
			if err := validateAttributes(item, path+"."+key); err != nil {
				return err
			}
		}
		return nil
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Errorf("%s has unsupported type %T", path, typed)
		}
		var normalized any
		if err := json.Unmarshal(data, &normalized); err != nil {
			return err
		}
		return validateAttributes(normalized, path)
	}
}

func (s *Snapshot) Normalize() {
	sort.Slice(s.Evidence, func(i, j int) bool { return s.Evidence[i].ID < s.Evidence[j].ID })
	sort.Slice(s.Entities, func(i, j int) bool { return s.Entities[i].ID < s.Entities[j].ID })
	sort.Slice(s.Relationships, func(i, j int) bool { return s.Relationships[i].ID < s.Relationships[j].ID })
	for i := range s.Entities {
		sort.Strings(s.Entities[i].EvidenceRefs)
		sort.Strings(s.Entities[i].Provenance)
		s.Entities[i].Name = strings.TrimSpace(s.Entities[i].Name)
	}
}
