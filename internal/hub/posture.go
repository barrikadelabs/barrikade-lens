package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/jackc/pgx/v5"
)

type aggregateEntity struct {
	Name         string
	Kind         string
	CanonicalKey string
	Attributes   map[string]any
	Confidence   string
	Provenance   []string
}

type observation struct {
	SourceID   string
	Name       string
	Kind       string
	Canonical  string
	Attributes map[string]any
	Confidence string
	Provenance []string
	LastSeen   time.Time
}

type changeMetadata struct {
	Category string
	Summary  string
	Details  map[string]any
}

type aggregateRelationship struct {
	Kind       string
	From       string
	To         string
	Attributes map[string]any
	Confidence string
}

func aggregateRelationshipObservations(ctx context.Context, tx pgx.Tx, organizationID, relationshipID string) (aggregateRelationship, error) {
	rows, err := tx.Query(ctx, `SELECT COALESCE(observation_kind,''),COALESCE(from_entity,''),COALESCE(to_entity,''),attributes,COALESCE(confidence,'possible')
		FROM source_relationships WHERE organization_id=$1 AND relationship_id=$2 AND current=true
		ORDER BY CASE COALESCE(confidence,'possible') WHEN 'confirmed' THEN 3 WHEN 'likely' THEN 2 ELSE 1 END DESC,last_seen_at DESC,source_id`, organizationID, relationshipID)
	if err != nil {
		return aggregateRelationship{}, err
	}
	defer rows.Close()
	var result aggregateRelationship
	first := true
	for rows.Next() {
		var kind, from, to, confidence string
		var encoded []byte
		if err := rows.Scan(&kind, &from, &to, &encoded, &confidence); err != nil {
			return aggregateRelationship{}, err
		}
		attributes := map[string]any{}
		if err := json.Unmarshal(encoded, &attributes); err != nil {
			return aggregateRelationship{}, err
		}
		if first {
			result = aggregateRelationship{Kind: kind, From: from, To: to, Attributes: attributes, Confidence: confidence}
			first = false
			continue
		}
		for key, value := range attributes {
			current, exists := result.Attributes[key]
			if !exists {
				result.Attributes[key] = cloneJSONValue(value)
				continue
			}
			if left, ok := current.(bool); ok {
				if right, ok := value.(bool); ok {
					result.Attributes[key] = left || right
					continue
				}
			}
			if merged, ok := mergeJSONArrays(current, value); ok {
				result.Attributes[key] = merged
			}
		}
	}
	if err := rows.Err(); err != nil {
		return aggregateRelationship{}, err
	}
	if first {
		return aggregateRelationship{}, pgx.ErrNoRows
	}
	return result, nil
}

func aggregateEntityObservations(ctx context.Context, tx pgx.Tx, organizationID, entityID string) (aggregateEntity, error) {
	rows, err := tx.Query(ctx, `SELECT source_id,COALESCE(observation_name,''),COALESCE(observation_kind,''),COALESCE(canonical_key,''),attributes,COALESCE(confidence,'possible'),provenance,last_seen_at
		FROM source_entities WHERE organization_id=$1 AND entity_id=$2 AND current=true
		ORDER BY CASE COALESCE(confidence,'possible') WHEN 'confirmed' THEN 3 WHEN 'likely' THEN 2 ELSE 1 END DESC,last_seen_at DESC,source_id`, organizationID, entityID)
	if err != nil {
		return aggregateEntity{}, err
	}
	defer rows.Close()
	items := []observation{}
	for rows.Next() {
		var item observation
		var attributes []byte
		if err := rows.Scan(&item.SourceID, &item.Name, &item.Kind, &item.Canonical, &attributes, &item.Confidence, &item.Provenance, &item.LastSeen); err != nil {
			return aggregateEntity{}, err
		}
		if err := json.Unmarshal(attributes, &item.Attributes); err != nil {
			return aggregateEntity{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return aggregateEntity{}, err
	}
	if len(items) == 0 {
		return aggregateEntity{}, pgx.ErrNoRows
	}
	result := aggregateEntity{Name: items[0].Name, Kind: items[0].Kind, CanonicalKey: items[0].Canonical, Attributes: map[string]any{}, Confidence: items[0].Confidence}
	provenance := map[string]struct{}{}
	type conflictValue struct {
		Value   any
		Sources map[string]struct{}
	}
	conflicts := map[string]map[string]*conflictValue{}
	selectedSource := map[string]string{}
	for _, item := range items {
		if confidenceRank(item.Confidence) > confidenceRank(result.Confidence) {
			result.Confidence = item.Confidence
		}
		for _, value := range item.Provenance {
			if value != "" {
				provenance[value] = struct{}{}
			}
		}
		for key, value := range item.Attributes {
			current, exists := result.Attributes[key]
			if !exists {
				result.Attributes[key] = cloneJSONValue(value)
				selectedSource[key] = item.SourceID
				continue
			}
			if left, ok := current.(bool); ok {
				if right, ok := value.(bool); ok {
					result.Attributes[key] = left || right
					continue
				}
			}
			if merged, ok := mergeJSONArrays(current, value); ok {
				result.Attributes[key] = merged
				continue
			}
			if reflect.DeepEqual(current, value) {
				continue
			}
			if conflicts[key] == nil {
				conflicts[key] = map[string]*conflictValue{}
			}
			encodedCurrent := canonicalJSON(current)
			if conflicts[key][encodedCurrent] == nil {
				conflicts[key][encodedCurrent] = &conflictValue{Value: current, Sources: map[string]struct{}{}}
			}
			conflicts[key][encodedCurrent].Sources[selectedSource[key]] = struct{}{}
			encoded := canonicalJSON(value)
			if conflicts[key][encoded] == nil {
				conflicts[key][encoded] = &conflictValue{Value: value, Sources: map[string]struct{}{}}
			}
			conflicts[key][encoded].Sources[item.SourceID] = struct{}{}
		}
	}
	for item := range provenance {
		result.Provenance = append(result.Provenance, item)
	}
	sort.Strings(result.Provenance)
	if _, err := tx.Exec(ctx, `DELETE FROM data_quality_conflicts WHERE organization_id=$1 AND entity_id=$2`, organizationID, entityID); err != nil {
		return aggregateEntity{}, err
	}
	for path, values := range conflicts {
		observed := []any{}
		sources := map[string]struct{}{}
		for _, item := range values {
			observed = append(observed, item.Value)
			for source := range item.Sources {
				sources[source] = struct{}{}
			}
		}
		sourceIDs := make([]string, 0, len(sources))
		for source := range sources {
			sourceIDs = append(sourceIDs, source)
		}
		sort.Strings(sourceIDs)
		encoded, _ := json.Marshal(observed)
		if _, err := tx.Exec(ctx, `INSERT INTO data_quality_conflicts(organization_id,entity_id,attribute_path,observed_values,source_ids) VALUES($1,$2,$3,$4,$5)`, organizationID, entityID, "attributes."+path, encoded, sourceIDs); err != nil {
			return aggregateEntity{}, err
		}
	}
	return result, nil
}

func materialDigest(value any) string {
	encoded, _ := json.Marshal(value)
	return discovery.ContentHash(encoded)
}

func entityChange(previousName, previousConfidence string, previousAttributes map[string]any, previousCurrent, previousStale bool, next aggregateEntity) *changeMetadata {
	fields := []map[string]any{}
	category := "metadata"
	if previousName != next.Name {
		fields = append(fields, map[string]any{"path": "name", "before": previousName, "after": next.Name})
		category = "identity"
	}
	if previousConfidence != next.Confidence {
		fields = append(fields, map[string]any{"path": "confidence", "before": previousConfidence, "after": next.Confidence})
		category = "confidence"
	}
	keys := map[string]struct{}{}
	for key := range previousAttributes {
		keys[key] = struct{}{}
	}
	for key := range next.Attributes {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		before, beforeOK := previousAttributes[key]
		after, afterOK := next.Attributes[key]
		if beforeOK == afterOK && reflect.DeepEqual(before, after) {
			continue
		}
		fields = append(fields, map[string]any{"path": "attributes." + key, "before": before, "after": after})
		if postureAttribute(key) {
			category = "state"
		} else if networkAttribute(key) {
			category = "network_scope"
		}
	}
	if !previousCurrent || previousStale {
		fields = append(fields, map[string]any{"path": "current", "before": previousCurrent, "after": true})
		category = "freshness"
	}
	if len(fields) == 0 {
		return nil
	}
	summary := "Discovery facts changed"
	beforeState, afterState := discoveryState(previousAttributes), discoveryState(next.Attributes)
	if beforeState != afterState {
		summary = titleState(beforeState) + " → " + titleState(afterState)
		category = "state"
	} else if category == "network_scope" {
		beforeNetwork, afterNetwork := networkScope("", previousAttributes), networkScope("", next.Attributes)
		if beforeNetwork != afterNetwork {
			summary = networkScopeTitle(beforeNetwork) + " → " + networkScopeTitle(afterNetwork)
		} else {
			summary = "Network exposure changed"
		}
	} else if category == "confidence" {
		summary = strings.Title(previousConfidence) + " → " + strings.Title(next.Confidence)
	}
	return &changeMetadata{Category: category, Summary: summary, Details: map[string]any{"fields": fields}}
}

func refreshEntityPosture(ctx context.Context, tx pgx.Tx, organizationID, entityID string) error {
	var kind, name, confidence string
	var attributes []byte
	var current bool
	var firstSeen, lastSeen time.Time
	if err := tx.QueryRow(ctx, `SELECT kind,name,attributes,confidence,current,first_seen_at,last_seen_at FROM entities WHERE organization_id=$1 AND id=$2`, organizationID, entityID).Scan(&kind, &name, &attributes, &confidence, &current, &firstSeen, &lastSeen); err != nil {
		return err
	}
	attrs := map[string]any{}
	_ = json.Unmarshal(attributes, &attrs)
	var targetID, surface *string
	_ = tx.QueryRow(ctx, `SELECT CASE WHEN COUNT(DISTINCT s.target_id)=1 THEN MIN(s.target_id) END,CASE WHEN COUNT(DISTINCT s.source_type)=1 THEN MIN(s.source_type) END
		FROM source_entities se JOIN sources s ON s.organization_id=se.organization_id AND s.id=se.source_id
		WHERE se.organization_id=$1 AND se.entity_id=$2 AND se.current=true`, organizationID, entityID).Scan(&targetID, &surface)
	role, systemType := postureRole(kind, attrs)
	state := discoveryState(attrs)
	network := networkScope(kind, attrs)
	if role == "system" {
		connectedNetwork, err := connectedNetworkScope(ctx, tx, organizationID, entityID)
		if err != nil {
			return err
		}
		network = strongerNetworkScope(network, connectedNetwork)
	}
	var attributed bool
	_ = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM relationships r WHERE r.organization_id=$1 AND r.current=true AND r.kind='owned_by' AND r.from_entity=$2 AND COALESCE((r.attributes->>'authoritative')::boolean,false)=true
		UNION ALL
		SELECT 1 FROM relationships d JOIN relationships o ON o.organization_id=d.organization_id AND o.current=true AND o.kind='owned_by' AND o.from_entity=d.to_entity
		WHERE d.organization_id=$1 AND d.current=true AND d.kind='defined_in' AND d.from_entity=$2 AND COALESCE((o.attributes->>'authoritative')::boolean,false)=true
	)`, organizationID, entityID).Scan(&attributed)
	productID, _ := attrs["product_id"].(string)
	productCategory, _ := attrs["product_category"].(string)
	surfaceValue := "mixed"
	if surface != nil && *surface != "" {
		surfaceValue = *surface
	} else if value, ok := attrs["source_surface"].(string); ok {
		surfaceValue = value
	}
	digest := materialDigest(map[string]any{"target_id": targetID, "surface": surfaceValue, "role": role, "system_type": systemType, "state": state, "network": network, "attributed": attributed, "confidence": confidence, "product_id": productID, "product_category": productCategory, "current": current})
	_, err := tx.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,target_id,surface,system_role,system_type,product_id,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT(organization_id,entity_id) DO UPDATE SET target_id=EXCLUDED.target_id,surface=EXCLUDED.surface,system_role=EXCLUDED.system_role,system_type=EXCLUDED.system_type,product_id=EXCLUDED.product_id,product_category=EXCLUDED.product_category,discovery_state=EXCLUDED.discovery_state,network_scope=EXCLUDED.network_scope,attributed=EXCLUDED.attributed,confidence=EXCLUDED.confidence,current=EXCLUDED.current,last_seen_at=EXCLUDED.last_seen_at,material_digest=EXCLUDED.material_digest`, organizationID, entityID, targetID, surfaceValue, role, nullString(systemType), nullString(productID), nullString(productCategory), state, network, attributed, confidence, current, firstSeen, lastSeen, digest)
	return err
}

func recomputeEntityFromCurrentObservations(ctx context.Context, tx pgx.Tx, organizationID, entityID string) (bool, error) {
	aggregated, err := aggregateEntityObservations(ctx, tx, organizationID, entityID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `UPDATE entities SET current=false,stale=true WHERE organization_id=$1 AND id=$2`, organizationID, entityID); err != nil {
			return false, err
		}
		return false, refreshEntityPosture(ctx, tx, organizationID, entityID)
	}
	if err != nil {
		return false, err
	}
	attributes, _ := json.Marshal(aggregated.Attributes)
	_, err = tx.Exec(ctx, `UPDATE entities e SET kind=$3,canonical_key=$4,name=$5,attributes=$6,confidence=$7,provenance=$8,current=true,stale=NOT EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current AND NOT se.stale),last_seen_at=(SELECT max(last_seen_at) FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current) WHERE organization_id=$1 AND id=$2`, organizationID, entityID, aggregated.Kind, nullString(aggregated.CanonicalKey), aggregated.Name, attributes, aggregated.Confidence, nonNilStrings(aggregated.Provenance))
	if err != nil {
		return false, err
	}
	return true, refreshEntityPosture(ctx, tx, organizationID, entityID)
}

func recomputeRelationshipFromCurrentObservations(ctx context.Context, tx pgx.Tx, organizationID, relationshipID string) (bool, error) {
	aggregated, err := aggregateRelationshipObservations(ctx, tx, organizationID, relationshipID)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `UPDATE relationships SET current=false,stale=true WHERE organization_id=$1 AND id=$2`, organizationID, relationshipID)
		return false, err
	}
	if err != nil {
		return false, err
	}
	attributes, _ := json.Marshal(aggregated.Attributes)
	_, err = tx.Exec(ctx, `UPDATE relationships r SET kind=$3,from_entity=$4,to_entity=$5,attributes=$6,confidence=$7,current=true,stale=NOT EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id AND sr.current AND NOT sr.stale),last_seen_at=(SELECT max(last_seen_at) FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id AND sr.current) WHERE organization_id=$1 AND id=$2`, organizationID, relationshipID, aggregated.Kind, aggregated.From, aggregated.To, attributes, aggregated.Confidence)
	return true, err
}

func postureRole(kind string, attrs map[string]any) (string, string) {
	if kind == string(discovery.KindAgent) {
		return "system", "autonomous_agent"
	}
	if kind == string(discovery.KindRuntime) {
		switch attrs["product_category"] {
		case "agent_tool":
			return "system", "agent_tool"
		case "model_runtime":
			return "system", "model_runtime"
		default:
			return "supporting", ""
		}
	}
	if kind == string(discovery.KindModelServer) {
		return "component", ""
	}
	if kind == string(discovery.KindEndpoint) || kind == string(discovery.KindRepository) || kind == string(discovery.KindCluster) {
		return "target", ""
	}
	if kind == string(discovery.KindModel) {
		return "artifact", ""
	}
	return "component", ""
}

func discoveryState(attrs map[string]any) string {
	for _, item := range []struct{ Key, State string }{{"running_at_scan", "running"}, {"deployed", "deployed"}, {"defined", "defined"}, {"configured", "configured"}, {"installed", "installed"}, {"state_present", "residual"}, {"cached", "cached"}} {
		if value, ok := attrs[item.Key].(bool); ok && value {
			return item.State
		}
	}
	return "observed"
}

func networkScope(kind string, attrs map[string]any) string {
	if external, _ := attrs["external"].(bool); external {
		return "external"
	}
	if serviceKind, _ := attrs["service_kind"].(string); strings.EqualFold(serviceKind, "LoadBalancer") || strings.EqualFold(serviceKind, "Ingress") {
		return "external"
	}
	switch attrs["binding"] {
	case "loopback":
		return "loopback"
	case "interface", "all_interfaces":
		return "network"
	}
	if kind == string(discovery.KindModelServer) || kind == string(discovery.KindMCPServer) || kind == string(discovery.KindAPIService) {
		if running, _ := attrs["running_at_scan"].(bool); running {
			return "unknown"
		}
	}
	return "none"
}

// connectedNetworkScope rolls the strongest factual binding of a directly
// connected server or service up to its root system. The component remains in
// the canonical graph; this is only an executive projection.
func connectedNetworkScope(ctx context.Context, tx pgx.Tx, organizationID, entityID string) (string, error) {
	rows, err := tx.Query(ctx, `SELECT e.kind,e.attributes
		FROM relationships r
		JOIN entities e ON e.organization_id=r.organization_id
			AND e.id=CASE WHEN r.from_entity=$2 THEN r.to_entity ELSE r.from_entity END
		WHERE r.organization_id=$1 AND r.current=true AND e.current=true
			AND (r.from_entity=$2 OR r.to_entity=$2)`, organizationID, entityID)
	if err != nil {
		return "none", err
	}
	defer rows.Close()
	result := "none"
	for rows.Next() {
		var kind string
		var encoded []byte
		if err := rows.Scan(&kind, &encoded); err != nil {
			return "none", err
		}
		attributes := map[string]any{}
		if err := json.Unmarshal(encoded, &attributes); err != nil {
			return "none", err
		}
		result = strongerNetworkScope(result, networkScope(kind, attributes))
	}
	return result, rows.Err()
}

func strongerNetworkScope(left, right string) string {
	rank := map[string]int{"none": 1, "unknown": 2, "loopback": 3, "network": 4, "external": 5}
	if rank[right] > rank[left] {
		return right
	}
	if left == "" {
		return "none"
	}
	return left
}

func postureAttribute(key string) bool {
	switch key {
	case "running_at_scan", "deployed", "defined", "configured", "installed", "state_present", "cached", "enabled":
		return true
	default:
		return false
	}
}

func networkAttribute(key string) bool {
	switch key {
	case "binding", "host", "hosts", "port", "ports", "transport", "endpoint", "external", "service_kind":
		return true
	default:
		return false
	}
}

func confidenceRank(value string) int {
	switch value {
	case "confirmed":
		return 3
	case "likely":
		return 2
	default:
		return 1
	}
}

func mergeJSONArrays(left, right any) ([]any, bool) {
	leftArray, leftOK := left.([]any)
	rightArray, rightOK := right.([]any)
	if !leftOK || !rightOK {
		return nil, false
	}
	seen := map[string]any{}
	for _, value := range append(append([]any{}, leftArray...), rightArray...) {
		seen[canonicalJSON(value)] = value
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]any, 0, len(keys))
	for _, key := range keys {
		result = append(result, seen[key])
	}
	return result, true
}

func cloneJSONValue(value any) any {
	encoded, _ := json.Marshal(value)
	var copy any
	_ = json.Unmarshal(encoded, &copy)
	return copy
}

func canonicalJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func titleState(value string) string {
	if value == "" {
		return "Unknown"
	}
	return strings.ToUpper(value[:1]) + strings.ReplaceAll(value[1:], "_", " ")
}

func relationshipCategory(kind string) string {
	switch kind {
	case string(discovery.RelationshipOwnedBy):
		return "attribution"
	case string(discovery.RelationshipExposes):
		return "network_scope"
	case string(discovery.RelationshipUses), string(discovery.RelationshipConnectsTo), string(discovery.RelationshipProvides), string(discovery.RelationshipInvokes):
		return "capability"
	default:
		return "metadata"
	}
}

func relationshipSummary(kind string, added bool) string {
	action := " removed"
	if added {
		action = " added"
	}
	switch kind {
	case string(discovery.RelationshipOwnedBy):
		return "Attribution evidence" + action
	case string(discovery.RelationshipExposes):
		return "Exposure" + action
	case string(discovery.RelationshipUses), string(discovery.RelationshipConnectsTo), string(discovery.RelationshipProvides), string(discovery.RelationshipInvokes):
		return "Capability connection" + action
	case string(discovery.RelationshipDeployedAs):
		return "Deployment link" + action
	default:
		return titleState(strings.ReplaceAll(kind, "_", " ")) + action
	}
}

func networkScopeTitle(value string) string {
	switch value {
	case "network":
		return "Network accessible"
	case "external":
		return "Externally accessible"
	case "loopback":
		return "Loopback"
	case "none":
		return "No listener"
	default:
		return "Unknown network scope"
	}
}

func validatePostureValue(value string) error {
	if value == "" {
		return fmt.Errorf("posture value is empty")
	}
	return nil
}
