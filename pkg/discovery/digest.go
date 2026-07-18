package discovery

import "encoding/json"

// Digest identifies inventory content while deliberately excluding delivery
// metadata. Managed collectors use it to avoid sending unchanged snapshots.
func (s Snapshot) Digest() (string, error) {
	copy := s
	copy.Evidence = append([]Evidence(nil), s.Evidence...)
	copy.Entities = append([]Entity(nil), s.Entities...)
	copy.Relationships = append([]Relationship(nil), s.Relationships...)
	copy.SnapshotID = ""
	copy.ObservedAt = ""
	copy.Sequence = 0
	for index := range copy.Evidence {
		copy.Evidence[index].ObservedAt = ""
	}
	copy.Normalize()
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	return ContentHash(data), nil
}
