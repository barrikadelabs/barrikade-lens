package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	PollInterval time.Duration
}

func (w Worker) Run(ctx context.Context) error {
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.PollInterval == 0 {
		w.PollInterval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			processed, err := w.processOne(ctx)
			if err != nil {
				w.Logger.Error("ingestion job failed", "error", err)
			}
			if processed {
				continue
			}
		}
	}
}

func (w Worker) processOne(ctx context.Context) (bool, error) {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var jobID uuid.UUID
	var payload []byte
	var attempts int
	err = tx.QueryRow(ctx, `SELECT id,payload,attempts FROM ingestion_jobs WHERE status='pending' AND next_attempt_at<=now() ORDER BY next_attempt_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&jobID, &payload, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE ingestion_jobs SET status='processing',started_at=now(),attempts=attempts+1 WHERE id=$1`, jobID); err != nil {
		return true, err
	}
	var snapshot discovery.Snapshot
	permanent := false
	if err = json.Unmarshal(payload, &snapshot); err == nil {
		if err = snapshot.Validate(); err != nil {
			permanent = true
		}
	} else {
		permanent = true
	}
	if err == nil {
		err = normalizeSnapshot(ctx, tx, snapshot)
		permanent = permanentNormalizationError(err)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		status := "pending"
		if permanent || attempts+1 >= 5 {
			status = "failed"
		}
		delay := time.Duration(1<<min(attempts, 6)) * time.Second
		_, markErr := w.Pool.Exec(ctx, `UPDATE ingestion_jobs SET status=$2,attempts=attempts+1,error_code=$3,error_message=$4,completed_at=CASE WHEN $2='failed' THEN now() ELSE NULL END,expires_at=CASE WHEN $2='failed' THEN now()+interval '24 hours' ELSE NULL END,next_attempt_at=CASE WHEN $2='pending' THEN now()+$5::interval ELSE next_attempt_at END WHERE id=$1`, jobID, status, "normalization_failed", safeError(err), fmt.Sprintf("%f seconds", delay.Seconds()))
		if markErr != nil {
			return true, fmt.Errorf("normalize: %v; mark failed: %w", err, markErr)
		}
		return true, err
	}
	if _, err = tx.Exec(ctx, `UPDATE ingestion_jobs SET status='complete',payload=NULL,error_code=NULL,error_message=NULL,completed_at=now(),expires_at=NULL WHERE id=$1`, jobID); err != nil {
		return true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, err
	}
	return true, nil
}

func permanentNormalizationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "out-of-order sequence") ||
		strings.Contains(message, "load source: no rows") ||
		strings.Contains(message, "violates foreign key constraint") ||
		strings.Contains(message, "invalid input syntax")
}

func normalizeSnapshot(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot) error {
	var lastSequence uint64
	var targetID string
	err := tx.QueryRow(ctx, `SELECT last_sequence,target_id FROM sources WHERE organization_id=$1 AND id=$2 FOR UPDATE`, snapshot.OrganizationID, snapshot.SourceID).Scan(&lastSequence, &targetID)
	if err != nil {
		return fmt.Errorf("load source: %w", err)
	}
	if snapshot.TargetID != targetID {
		return fmt.Errorf("snapshot target_id does not match enrolled source target")
	}
	if snapshot.Sequence > 0 && snapshot.Sequence <= lastSequence {
		return fmt.Errorf("out-of-order sequence %d; source is at %d", snapshot.Sequence, lastSequence)
	}
	sequence := snapshot.Sequence
	if sequence == 0 {
		sequence = lastSequence + 1
		snapshot.Sequence = sequence
	}
	observedAt, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt)
	if err != nil {
		return err
	}
	coverageJSON, err := json.Marshal(snapshot.Coverage)
	if err != nil {
		return err
	}
	partial := snapshot.Coverage.Partial || len(snapshot.Errors) > 0
	if snapshot.Full {
		_, err = tx.Exec(ctx, `UPDATE sources SET last_sequence=$3,last_full_sequence=$3,last_seen_at=$4,last_full_at=$4,collector_version=$5,latest_coverage=$6,latest_partial=$7,latest_error_count=$8 WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, snapshot.SourceID, sequence, observedAt, snapshot.Collector.Version, coverageJSON, partial, len(snapshot.Errors))
	} else {
		_, err = tx.Exec(ctx, `UPDATE sources SET last_sequence=$3,last_seen_at=$4,collector_version=$5,latest_coverage=$6,latest_partial=$7,latest_error_count=$8 WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, snapshot.SourceID, sequence, observedAt, snapshot.Collector.Version, coverageJSON, partial, len(snapshot.Errors))
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE discovery_targets SET name=COALESCE(NULLIF($3,''),name),platform=COALESCE(NULLIF($4,''),platform),last_seen_at=$5,last_full_at=CASE WHEN $6 THEN $5 ELSE last_full_at END,current=true WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, targetID, snapshot.Scope.Name, snapshot.Scope.Attributes["platform"], observedAt, snapshot.Full)
	if err != nil {
		return err
	}
	scanDigest := materialDigest(map[string]any{"full": snapshot.Full, "coverage": snapshot.Coverage, "errors": snapshot.Errors})
	_, err = tx.Exec(ctx, `INSERT INTO source_scans(organization_id,source_id,target_id,snapshot_id,observed_at,is_full,sequence,partial,coverage,error_count,material_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(organization_id,snapshot_id) DO NOTHING`, snapshot.OrganizationID, snapshot.SourceID, targetID, snapshot.SnapshotID, observedAt, snapshot.Full, sequence, partial, coverageJSON, len(snapshot.Errors), scanDigest)
	if err != nil {
		return err
	}

	evidenceEntities := map[string][]string{}
	evidenceRelationships := map[string][]string{}
	touchedEntities := map[string]struct{}{}
	discoveredEntities := map[string]struct{}{}
	for _, entity := range snapshot.Entities {
		for _, ref := range entity.EvidenceRefs {
			evidenceEntities[ref] = append(evidenceEntities[ref], entity.ID)
		}
		event, metadata, err := upsertEntity(ctx, tx, snapshot, entity, observedAt, sequence)
		if err != nil {
			return err
		}
		touchedEntities[entity.ID] = struct{}{}
		if event != "" {
			if event == "entity.discovered" {
				discoveredEntities[entity.ID] = struct{}{}
			}
			if err := recordChange(ctx, tx, snapshot, event, entity.ID, metadata); err != nil {
				return err
			}
			if event == "entity.updated" && metadata != nil && metadata.Category == "network_scope" {
				if err := recordConnectedSystemChanges(ctx, tx, snapshot, entity.ID, metadata); err != nil {
					return err
				}
			}
		}
	}
	for _, relationship := range snapshot.Relationships {
		for _, ref := range relationship.EvidenceRefs {
			evidenceRelationships[ref] = append(evidenceRelationships[ref], relationship.ID)
		}
		metadata, err := upsertRelationship(ctx, tx, snapshot, relationship, observedAt, sequence)
		if err != nil {
			return err
		}
		touchedEntities[relationship.From] = struct{}{}
		touchedEntities[relationship.To] = struct{}{}
		_, rootWasDiscovered := discoveredEntities[relationship.From]
		if metadata != nil && !rootWasDiscovered {
			if err := recordChange(ctx, tx, snapshot, "entity.updated", relationship.From, metadata); err != nil {
				return err
			}
		}
	}
	for _, evidence := range snapshot.Evidence {
		when, err := time.Parse(time.RFC3339Nano, evidence.ObservedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO evidence_observations(organization_id,snapshot_id,evidence_id,source_id,entity_ids,relationship_ids,detector_id,detector_version,method,family,specificity,locator,content_hash,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT DO NOTHING`, snapshot.OrganizationID, snapshot.SnapshotID, evidence.ID, snapshot.SourceID, nonNilStrings(evidenceEntities[evidence.ID]), nonNilStrings(evidenceRelationships[evidence.ID]), evidence.DetectorID, evidence.DetectorVersion, evidence.Method, evidence.Family, evidence.Specificity, nullString(evidence.Locator), nullString(evidence.ContentHash), when)
		if err != nil {
			return err
		}
	}
	if snapshot.Full {
		if err := applyFullMisses(ctx, tx, snapshot, sequence); err != nil {
			return err
		}
	}
	for entityID := range touchedEntities {
		if err := refreshEntityPosture(ctx, tx, snapshot.OrganizationID, entityID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	_, _ = tx.Exec(ctx, `DELETE FROM evidence_observations WHERE expires_at < now()`)
	_, _ = tx.Exec(ctx, `DELETE FROM ingestion_jobs WHERE status='failed' AND expires_at < now()`)
	_, _ = tx.Exec(ctx, `DELETE FROM ingestion_jobs WHERE status='complete' AND completed_at < now()-interval '90 days'`)
	_, _ = tx.Exec(ctx, `DELETE FROM changes WHERE changed_at < now()-interval '90 days'`)
	_, _ = tx.Exec(ctx, `DELETE FROM webhook_outbox WHERE delivered_at < now()-interval '90 days'`)
	return nil
}

func upsertEntity(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, entity discovery.Entity, observedAt time.Time, sequence uint64) (string, *changeMetadata, error) {
	var previousName, previousConfidence string
	var previousAttributes []byte
	var previousCurrent, previousStale bool
	err := tx.QueryRow(ctx, `SELECT name,attributes,confidence,current,stale FROM entities WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, entity.ID).Scan(&previousName, &previousAttributes, &previousConfidence, &previousCurrent, &previousStale)
	discovered := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !discovered {
		return "", nil, err
	}
	attributes, err := json.Marshal(entity.Attributes)
	if err != nil {
		return "", nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true,false,$9,$9) ON CONFLICT(organization_id,id) DO NOTHING`, snapshot.OrganizationID, entity.ID, entity.Kind, nullString(entity.CanonicalKey), entity.Name, attributes, entity.Confidence, nonNilStrings(entity.Provenance), observedAt)
	if err != nil {
		return "", nil, err
	}
	digest := materialDigest(map[string]any{"name": entity.Name, "kind": entity.Kind, "canonical_key": entity.CanonicalKey, "attributes": entity.Attributes, "confidence": entity.Confidence, "provenance": entity.Provenance})
	_, err = tx.Exec(ctx, `INSERT INTO source_entities(organization_id,source_id,entity_id,last_seen_at,last_seen_sequence,consecutive_full_misses,current,stale,observation_name,observation_kind,canonical_key,attributes,confidence,provenance,material_digest) VALUES($1,$2,$3,$4,$5,0,true,false,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(organization_id,source_id,entity_id) DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at,last_seen_sequence=EXCLUDED.last_seen_sequence,consecutive_full_misses=0,current=true,stale=false,observation_name=EXCLUDED.observation_name,observation_kind=EXCLUDED.observation_kind,canonical_key=EXCLUDED.canonical_key,attributes=EXCLUDED.attributes,confidence=EXCLUDED.confidence,provenance=EXCLUDED.provenance,material_digest=EXCLUDED.material_digest`, snapshot.OrganizationID, snapshot.SourceID, entity.ID, observedAt, sequence, entity.Name, entity.Kind, nullString(entity.CanonicalKey), attributes, entity.Confidence, nonNilStrings(entity.Provenance), digest)
	if err != nil {
		return "", nil, err
	}
	aggregated, err := aggregateEntityObservations(ctx, tx, snapshot.OrganizationID, entity.ID)
	if err != nil {
		return "", nil, err
	}
	aggregatedAttributes, err := json.Marshal(aggregated.Attributes)
	if err != nil {
		return "", nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE entities SET kind=$3,canonical_key=$4,name=$5,attributes=$6,confidence=$7,provenance=$8,current=true,stale=false,last_seen_at=$9 WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, entity.ID, aggregated.Kind, nullString(aggregated.CanonicalKey), aggregated.Name, aggregatedAttributes, aggregated.Confidence, nonNilStrings(aggregated.Provenance), observedAt)
	if err != nil {
		return "", nil, err
	}
	if discovered {
		return "entity.discovered", &changeMetadata{Category: "identity", Summary: "System discovered"}, nil
	}
	var prior map[string]any
	_ = json.Unmarshal(previousAttributes, &prior)
	if metadata := entityChange(previousName, previousConfidence, prior, previousCurrent, previousStale, aggregated); metadata != nil {
		return "entity.updated", metadata, nil
	}
	return "", nil, nil
}

func upsertRelationship(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, relation discovery.Relationship, observedAt time.Time, sequence uint64) (*changeMetadata, error) {
	attributes, err := json.Marshal(relation.Attributes)
	if err != nil {
		return nil, err
	}
	var previousKind, previousFrom, previousTo, previousConfidence string
	var previousAttributes []byte
	var previousCurrent, previousStale bool
	lookupErr := tx.QueryRow(ctx, `SELECT kind,from_entity,to_entity,attributes,confidence,current,stale FROM relationships WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, relation.ID).Scan(&previousKind, &previousFrom, &previousTo, &previousAttributes, &previousConfidence, &previousCurrent, &previousStale)
	discovered := errors.Is(lookupErr, pgx.ErrNoRows)
	if lookupErr != nil && !discovered {
		return nil, lookupErr
	}
	_, err = tx.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,$3,$4,$5,$6,$7,true,false,$8,$8) ON CONFLICT(organization_id,id) DO NOTHING`, snapshot.OrganizationID, relation.ID, relation.Kind, relation.From, relation.To, attributes, relation.Confidence, observedAt)
	if err != nil {
		return nil, err
	}
	digest := materialDigest(map[string]any{"kind": relation.Kind, "from": relation.From, "to": relation.To, "attributes": relation.Attributes, "confidence": relation.Confidence})
	_, err = tx.Exec(ctx, `INSERT INTO source_relationships(organization_id,source_id,relationship_id,last_seen_at,last_seen_sequence,consecutive_full_misses,current,stale,observation_kind,from_entity,to_entity,attributes,confidence,material_digest) VALUES($1,$2,$3,$4,$5,0,true,false,$6,$7,$8,$9,$10,$11) ON CONFLICT(organization_id,source_id,relationship_id) DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at,last_seen_sequence=EXCLUDED.last_seen_sequence,consecutive_full_misses=0,current=true,stale=false,observation_kind=EXCLUDED.observation_kind,from_entity=EXCLUDED.from_entity,to_entity=EXCLUDED.to_entity,attributes=EXCLUDED.attributes,confidence=EXCLUDED.confidence,material_digest=EXCLUDED.material_digest`, snapshot.OrganizationID, snapshot.SourceID, relation.ID, observedAt, sequence, relation.Kind, relation.From, relation.To, attributes, relation.Confidence, digest)
	if err != nil {
		return nil, err
	}
	aggregated, err := aggregateRelationshipObservations(ctx, tx, snapshot.OrganizationID, relation.ID)
	if err != nil {
		return nil, err
	}
	aggregatedAttributes, _ := json.Marshal(aggregated.Attributes)
	_, err = tx.Exec(ctx, `UPDATE relationships SET kind=$3,from_entity=$4,to_entity=$5,attributes=$6,confidence=$7,current=true,stale=false,last_seen_at=$8 WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, relation.ID, aggregated.Kind, aggregated.From, aggregated.To, aggregatedAttributes, aggregated.Confidence, observedAt)
	if err != nil {
		return nil, err
	}
	category := relationshipCategory(aggregated.Kind)
	if discovered {
		return &changeMetadata{Category: category, Summary: relationshipSummary(aggregated.Kind, true), Details: map[string]any{"relationship_id": relation.ID, "relationship_kind": aggregated.Kind, "to_entity": aggregated.To, "change": "added"}}, nil
	}
	previousDigest := materialDigest(map[string]any{"kind": previousKind, "from": previousFrom, "to": previousTo, "attributes": jsonObject(previousAttributes), "confidence": previousConfidence, "current": previousCurrent, "stale": previousStale})
	nextDigest := materialDigest(map[string]any{"kind": aggregated.Kind, "from": aggregated.From, "to": aggregated.To, "attributes": aggregated.Attributes, "confidence": aggregated.Confidence, "current": true, "stale": false})
	if previousDigest == nextDigest {
		return nil, nil
	}
	return &changeMetadata{Category: category, Summary: relationshipSummary(aggregated.Kind, false), Details: map[string]any{"relationship_id": relation.ID, "relationship_kind": aggregated.Kind, "to_entity": aggregated.To, "change": "updated", "before": map[string]any{"attributes": jsonObject(previousAttributes), "confidence": previousConfidence}, "after": map[string]any{"attributes": aggregated.Attributes, "confidence": aggregated.Confidence}}}, nil
}

func applyFullMisses(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, sequence uint64) error {
	rows, err := tx.Query(ctx, `UPDATE source_entities SET consecutive_full_misses=consecutive_full_misses+1,stale=(consecutive_full_misses+1>=2),current=(consecutive_full_misses+1<3) WHERE organization_id=$1 AND source_id=$2 AND last_seen_sequence<>$3 AND current=true RETURNING entity_id,consecutive_full_misses`, snapshot.OrganizationID, snapshot.SourceID, sequence)
	if err != nil {
		return err
	}
	defer rows.Close()
	type missed struct {
		id    string
		count int
	}
	items := []missed{}
	for rows.Next() {
		var item missed
		if err := rows.Scan(&item.id, &item.count); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		var wasCurrent, wasStale bool
		if err := tx.QueryRow(ctx, `SELECT current,stale FROM entities WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, item.id).Scan(&wasCurrent, &wasStale); err != nil {
			return err
		}
		aggregated, aggregateErr := aggregateEntityObservations(ctx, tx, snapshot.OrganizationID, item.id)
		if aggregateErr == nil {
			attributes, _ := json.Marshal(aggregated.Attributes)
			_, err = tx.Exec(ctx, `UPDATE entities e SET kind=$3,canonical_key=$4,name=$5,attributes=$6,confidence=$7,provenance=$8,current=true,stale=NOT EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current AND NOT se.stale),last_seen_at=(SELECT max(last_seen_at) FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current) WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, item.id, aggregated.Kind, nullString(aggregated.CanonicalKey), aggregated.Name, attributes, aggregated.Confidence, nonNilStrings(aggregated.Provenance))
		} else if errors.Is(aggregateErr, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `UPDATE entities SET current=false,stale=true WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, item.id)
		} else {
			return aggregateErr
		}
		if err != nil {
			return err
		}
		var isCurrent, isStale bool
		if err := tx.QueryRow(ctx, `SELECT current,stale FROM entities WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, item.id).Scan(&isCurrent, &isStale); err != nil {
			return err
		}
		event := ""
		if wasCurrent && !wasStale && isCurrent && isStale {
			event = "entity.stale"
		} else if wasCurrent && !isCurrent {
			event = "entity.removed"
		}
		if event != "" {
			if err := recordChange(ctx, tx, snapshot, event, item.id, &changeMetadata{Category: "freshness", Summary: map[bool]string{true: "System is stale", false: "System removed from current inventory"}[isCurrent]}); err != nil {
				return err
			}
		}
		if err := refreshEntityPosture(ctx, tx, snapshot.OrganizationID, item.id); err != nil {
			return err
		}
	}
	relationRows, err := tx.Query(ctx, `UPDATE source_relationships SET consecutive_full_misses=consecutive_full_misses+1,stale=(consecutive_full_misses+1>=2),current=(consecutive_full_misses+1<3) WHERE organization_id=$1 AND source_id=$2 AND last_seen_sequence<>$3 AND current=true RETURNING relationship_id,consecutive_full_misses`, snapshot.OrganizationID, snapshot.SourceID, sequence)
	if err != nil {
		return err
	}
	defer relationRows.Close()
	relationIDs := []string{}
	for relationRows.Next() {
		var id string
		var count int
		if err := relationRows.Scan(&id, &count); err != nil {
			return err
		}
		relationIDs = append(relationIDs, id)
	}
	if err := relationRows.Err(); err != nil {
		return err
	}
	relationRows.Close()
	for _, relationshipID := range relationIDs {
		var fromEntity, toEntity, kind string
		var wasCurrent bool
		if err := tx.QueryRow(ctx, `SELECT from_entity,to_entity,kind,current FROM relationships WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, relationshipID).Scan(&fromEntity, &toEntity, &kind, &wasCurrent); err != nil {
			return err
		}
		aggregated, aggregateErr := aggregateRelationshipObservations(ctx, tx, snapshot.OrganizationID, relationshipID)
		if aggregateErr == nil {
			attributes, _ := json.Marshal(aggregated.Attributes)
			_, err = tx.Exec(ctx, `UPDATE relationships r SET kind=$3,from_entity=$4,to_entity=$5,attributes=$6,confidence=$7,current=true,stale=NOT EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id AND sr.current AND NOT sr.stale),last_seen_at=(SELECT max(last_seen_at) FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id AND sr.current) WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, relationshipID, aggregated.Kind, aggregated.From, aggregated.To, attributes, aggregated.Confidence)
		} else if errors.Is(aggregateErr, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `UPDATE relationships SET current=false,stale=true WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, relationshipID)
		} else {
			return aggregateErr
		}
		if err != nil {
			return err
		}
		var isCurrent bool
		if err := tx.QueryRow(ctx, `SELECT current FROM relationships WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, relationshipID).Scan(&isCurrent); err != nil {
			return err
		}
		if wasCurrent && !isCurrent {
			metadata := &changeMetadata{Category: relationshipCategory(kind), Summary: relationshipSummary(kind, false), Details: map[string]any{"relationship_id": relationshipID, "relationship_kind": kind, "to_entity": toEntity, "change": "removed"}}
			if err := recordChange(ctx, tx, snapshot, "entity.updated", fromEntity, metadata); err != nil {
				return err
			}
		}
		for _, entityID := range []string{fromEntity, toEntity} {
			if err := refreshEntityPosture(ctx, tx, snapshot.OrganizationID, entityID); err != nil {
				return err
			}
		}
	}
	return nil
}

func recordChange(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, event, entityID string, metadata ...*changeMetadata) error {
	changeID := uuid.New()
	details := map[string]any{"entity_id": entityID, "source_id": snapshot.SourceID, "snapshot_id": snapshot.SnapshotID}
	category, summary := "identity", "Discovery inventory changed"
	if len(metadata) > 0 && metadata[0] != nil {
		category, summary = metadata[0].Category, metadata[0].Summary
		for key, value := range metadata[0].Details {
			details[key] = value
		}
	}
	payload, err := json.Marshal(map[string]any{"id": changeID, "type": event, "organization_id": snapshot.OrganizationID, "occurred_at": time.Now().UTC().Format(time.RFC3339Nano), "data": details, "category": category, "summary": summary})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO changes(id,organization_id,source_id,entity_id,event_type,snapshot_id,details,category,summary) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, changeID, snapshot.OrganizationID, snapshot.SourceID, entityID, event, snapshot.SnapshotID, details, category, summary)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_outbox(id,webhook_id,organization_id,event_type,payload) SELECT gen_random_uuid(),id,organization_id,$2,$3 FROM webhook_endpoints WHERE organization_id=$1 AND enabled=true`, snapshot.OrganizationID, event, payload)
	return err
}

func recordConnectedSystemChanges(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, changedEntityID string, metadata *changeMetadata) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT p.entity_id
		FROM relationships r
		JOIN entity_posture p ON p.organization_id=r.organization_id
			AND p.entity_id=CASE WHEN r.from_entity=$2 THEN r.to_entity ELSE r.from_entity END
		WHERE r.organization_id=$1 AND r.current=true AND p.current=true AND p.system_role='system'
			AND (r.from_entity=$2 OR r.to_entity=$2)`, snapshot.OrganizationID, changedEntityID)
	if err != nil {
		return err
	}
	defer rows.Close()
	rootIDs := []string{}
	for rows.Next() {
		var rootID string
		if err := rows.Scan(&rootID); err != nil {
			return err
		}
		if rootID != changedEntityID {
			rootIDs = append(rootIDs, rootID)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, rootID := range rootIDs {
		if err := recordChange(ctx, tx, snapshot, "entity.updated", rootID, metadata); err != nil {
			return err
		}
	}
	return nil
}

func jsonObject(value []byte) map[string]any {
	result := map[string]any{}
	_ = json.Unmarshal(value, &result)
	return result
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
func safeError(err error) string {
	message := err.Error()
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}
