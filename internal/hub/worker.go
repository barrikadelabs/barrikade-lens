package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
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
	var lastSequence, lastFullSequence uint64
	err := tx.QueryRow(ctx, `SELECT last_sequence,last_full_sequence FROM sources WHERE organization_id=$1 AND id=$2 FOR UPDATE`, snapshot.OrganizationID, snapshot.SourceID).Scan(&lastSequence, &lastFullSequence)
	if err != nil {
		return fmt.Errorf("load source: %w", err)
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
	if snapshot.Full {
		_, err = tx.Exec(ctx, `UPDATE sources SET last_sequence=$3,last_full_sequence=$3,last_seen_at=$4,last_full_at=$4,collector_version=$5 WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, snapshot.SourceID, sequence, observedAt, snapshot.Collector.Version)
	} else {
		_, err = tx.Exec(ctx, `UPDATE sources SET last_sequence=$3,last_seen_at=$4,collector_version=$5 WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, snapshot.SourceID, sequence, observedAt, snapshot.Collector.Version)
	}
	if err != nil {
		return err
	}

	evidenceEntities := map[string][]string{}
	evidenceRelationships := map[string][]string{}
	for _, entity := range snapshot.Entities {
		for _, ref := range entity.EvidenceRefs {
			evidenceEntities[ref] = append(evidenceEntities[ref], entity.ID)
		}
		event, err := upsertEntity(ctx, tx, snapshot, entity, observedAt, sequence)
		if err != nil {
			return err
		}
		if event != "" {
			if err := recordChange(ctx, tx, snapshot, event, entity.ID); err != nil {
				return err
			}
		}
	}
	for _, relationship := range snapshot.Relationships {
		for _, ref := range relationship.EvidenceRefs {
			evidenceRelationships[ref] = append(evidenceRelationships[ref], relationship.ID)
		}
		if err := upsertRelationship(ctx, tx, snapshot, relationship, observedAt, sequence); err != nil {
			return err
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
	_, _ = tx.Exec(ctx, `DELETE FROM evidence_observations WHERE expires_at < now()`)
	_, _ = tx.Exec(ctx, `DELETE FROM ingestion_jobs WHERE status='failed' AND expires_at < now()`)
	_, _ = tx.Exec(ctx, `DELETE FROM ingestion_jobs WHERE status='complete' AND completed_at < now()-interval '90 days'`)
	_, _ = tx.Exec(ctx, `DELETE FROM changes WHERE changed_at < now()-interval '90 days'`)
	_, _ = tx.Exec(ctx, `DELETE FROM webhook_outbox WHERE delivered_at < now()-interval '90 days'`)
	return nil
}

func upsertEntity(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, entity discovery.Entity, observedAt time.Time, sequence uint64) (string, error) {
	var previousName, previousConfidence string
	var previousAttributes []byte
	var previousCurrent, previousStale bool
	err := tx.QueryRow(ctx, `SELECT name,attributes,confidence,current,stale FROM entities WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, entity.ID).Scan(&previousName, &previousAttributes, &previousConfidence, &previousCurrent, &previousStale)
	discovered := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !discovered {
		return "", err
	}
	attributes, err := json.Marshal(entity.Attributes)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true,false,$9,$9) ON CONFLICT(organization_id,id) DO UPDATE SET kind=EXCLUDED.kind,canonical_key=EXCLUDED.canonical_key,name=EXCLUDED.name,attributes=EXCLUDED.attributes,confidence=EXCLUDED.confidence,provenance=EXCLUDED.provenance,current=true,stale=false,last_seen_at=EXCLUDED.last_seen_at`, snapshot.OrganizationID, entity.ID, entity.Kind, nullString(entity.CanonicalKey), entity.Name, attributes, entity.Confidence, nonNilStrings(entity.Provenance), observedAt)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO source_entities(organization_id,source_id,entity_id,last_seen_at,last_seen_sequence,consecutive_full_misses,current,stale) VALUES($1,$2,$3,$4,$5,0,true,false) ON CONFLICT(organization_id,source_id,entity_id) DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at,last_seen_sequence=EXCLUDED.last_seen_sequence,consecutive_full_misses=0,current=true,stale=false`, snapshot.OrganizationID, snapshot.SourceID, entity.ID, observedAt, sequence)
	if err != nil {
		return "", err
	}
	if discovered {
		return "entity.discovered", nil
	}
	var prior map[string]any
	_ = json.Unmarshal(previousAttributes, &prior)
	if previousName != entity.Name || previousConfidence != string(entity.Confidence) || !reflect.DeepEqual(prior, entity.Attributes) || !previousCurrent || previousStale {
		return "entity.updated", nil
	}
	return "", nil
}

func upsertRelationship(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, relation discovery.Relationship, observedAt time.Time, sequence uint64) error {
	attributes, err := json.Marshal(relation.Attributes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,$3,$4,$5,$6,$7,true,false,$8,$8) ON CONFLICT(organization_id,id) DO UPDATE SET kind=EXCLUDED.kind,from_entity=EXCLUDED.from_entity,to_entity=EXCLUDED.to_entity,attributes=EXCLUDED.attributes,confidence=EXCLUDED.confidence,current=true,stale=false,last_seen_at=EXCLUDED.last_seen_at`, snapshot.OrganizationID, relation.ID, relation.Kind, relation.From, relation.To, attributes, relation.Confidence, observedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO source_relationships(organization_id,source_id,relationship_id,last_seen_at,last_seen_sequence,consecutive_full_misses,current,stale) VALUES($1,$2,$3,$4,$5,0,true,false) ON CONFLICT(organization_id,source_id,relationship_id) DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at,last_seen_sequence=EXCLUDED.last_seen_sequence,consecutive_full_misses=0,current=true,stale=false`, snapshot.OrganizationID, snapshot.SourceID, relation.ID, observedAt, sequence)
	return err
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
		_, err = tx.Exec(ctx, `UPDATE entities e SET current=EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current),stale=NOT EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current AND NOT se.stale) WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, item.id)
		if err != nil {
			return err
		}
		event := ""
		if item.count == 2 {
			event = "entity.stale"
		} else if item.count == 3 {
			event = "entity.removed"
		}
		if event != "" {
			if err := recordChange(ctx, tx, snapshot, event, item.id); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE source_relationships SET consecutive_full_misses=consecutive_full_misses+1,stale=(consecutive_full_misses+1>=2),current=(consecutive_full_misses+1<3) WHERE organization_id=$1 AND source_id=$2 AND last_seen_sequence<>$3 AND current=true`, snapshot.OrganizationID, snapshot.SourceID, sequence)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE relationships r SET current=EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id AND sr.current),stale=NOT EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id AND sr.current AND NOT sr.stale) WHERE r.organization_id=$1`, snapshot.OrganizationID)
	return err
}

func recordChange(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, event, entityID string) error {
	changeID := uuid.New()
	details := map[string]any{"entity_id": entityID, "source_id": snapshot.SourceID, "snapshot_id": snapshot.SnapshotID}
	payload, err := json.Marshal(map[string]any{"id": changeID, "type": event, "organization_id": snapshot.OrganizationID, "occurred_at": time.Now().UTC().Format(time.RFC3339Nano), "data": details})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO changes(id,organization_id,source_id,entity_id,event_type,snapshot_id,details) VALUES($1,$2,$3,$4,$5,$6,$7)`, changeID, snapshot.OrganizationID, snapshot.SourceID, entityID, event, snapshot.SnapshotID, details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_outbox(id,webhook_id,organization_id,event_type,payload) SELECT gen_random_uuid(),id,organization_id,$2,$3 FROM webhook_endpoints WHERE organization_id=$1 AND enabled=true`, snapshot.OrganizationID, event, payload)
	return err
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
