package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/cloud"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CloudWorker struct {
	Pool         *pgxpool.Pool
	Adapters     cloud.Registry
	Logger       *slog.Logger
	PollInterval time.Duration
}

func (w CloudWorker) Run(ctx context.Context) error {
	if w.Pool == nil {
		return fmt.Errorf("database pool is required")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.PollInterval == 0 {
		w.PollInterval = time.Second
	}
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.finalizeOne(ctx); err != nil {
				w.Logger.Warn("cloud scan finalization failed", "error", err)
			}
			if _, err := w.processOne(ctx); err != nil {
				w.Logger.Warn("cloud scan failed", "error", err)
			}
		case <-maintenance.C:
			if err := w.enqueueScheduled(ctx); err != nil {
				w.Logger.Warn("cloud scan scheduling failed", "error", err)
			}
			if err := w.purgeExpired(ctx); err != nil {
				w.Logger.Warn("environment retention purge failed", "error", err)
			}
		}
	}
}

type claimedCloudJob struct {
	ID          uuid.UUID
	Trigger     string
	Attempts    int
	QueuedAt    time.Time
	Environment cloud.Environment
}

func (w CloudWorker) processOne(ctx context.Context) (bool, error) {
	job, ok, err := w.claimOne(ctx)
	if err != nil || !ok {
		return ok, err
	}
	adapter, err := w.Adapters.Adapter(job.Environment.Provider)
	if err != nil {
		return true, w.failOrRetry(ctx, job, err)
	}
	scanContext, cancel := context.WithCancel(ctx)
	defer cancel()
	disconnected := make(chan struct{})
	go w.cancelWhenDisconnected(scanContext, cancel, job.Environment.OrganizationID, job.Environment.ID, disconnected)
	progress := func(value cloud.Progress) {
		encoded, _ := json.Marshal(value)
		_, _ = w.Pool.Exec(scanContext, `UPDATE cloud_scan_jobs SET phase=$2,progress=$3 WHERE id=$1 AND status='running'`, job.ID, value.Phase, encoded)
	}
	snapshot, scanErr := adapter.Scan(scanContext, job.Environment, 0, progress)
	close(disconnected)
	if scanContext.Err() != nil {
		_, _ = w.Pool.Exec(ctx, `UPDATE cloud_scan_jobs SET status='cancelled',phase='cancelled',completed_at=now(),error_code='disconnected',error_message='The environment was disconnected' WHERE id=$1 AND status IN ('running','queued')`, job.ID)
		return true, nil
	}
	if scanErr != nil && snapshot.SnapshotID == "" {
		return true, w.failOrRetry(ctx, job, scanErr)
	}
	if err := prepareCloudSnapshot(&snapshot, job.Environment); err != nil {
		return true, w.failOrRetry(ctx, job, &cloud.Error{Code: "invalid_provider_snapshot", Message: "A provider detector returned an invalid result", Cause: err})
	}
	if scanErr != nil {
		code, message, retryable, _ := cloud.SafeError(scanErr)
		snapshot.Coverage.Partial = true
		snapshot.Errors = append(snapshot.Errors, discovery.ScanError{Code: code, Message: message, Retryable: retryable})
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return true, w.failOrRetry(ctx, job, err)
	}
	ingestionID := uuid.New()
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, err
	}
	defer tx.Rollback(ctx)
	var connected bool
	if err = tx.QueryRow(ctx, `SELECT connection_status='connected' FROM environment_connections WHERE organization_id=$1 AND id=$2 FOR UPDATE`, job.Environment.OrganizationID, job.Environment.ID).Scan(&connected); err != nil || !connected {
		_, _ = tx.Exec(ctx, `UPDATE cloud_scan_jobs SET status='cancelled',phase='cancelled',completed_at=now() WHERE id=$1`, job.ID)
		return true, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `INSERT INTO ingestion_jobs(id,organization_id,source_id,snapshot_id,status,payload) VALUES($1,$2,$3,$4,'pending',$5)`, ingestionID, job.Environment.OrganizationID, job.Environment.SourceID, snapshot.SnapshotID, payload)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE cloud_scan_jobs SET status='ingesting',phase='normalizing',progress=$2,ingestion_job_id=$3,error_code=NULL,error_message=NULL WHERE id=$1`, job.ID, jsonBytes(map[string]any{"entities": len(snapshot.Entities), "relationships": len(snapshot.Relationships), "coverage": snapshot.Coverage}), ingestionID)
	}
	if err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func (w CloudWorker) claimOne(ctx context.Context) (claimedCloudJob, bool, error) {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return claimedCloudJob{}, false, err
	}
	defer tx.Rollback(ctx)
	var job claimedCloudJob
	var config []byte
	err = tx.QueryRow(ctx, `SELECT j.id,j.trigger,j.attempts,j.created_at,e.id::text,e.organization_id,e.provider,e.external_id,e.display_name,e.source_id,e.target_id,e.configuration
		FROM cloud_scan_jobs j JOIN environment_connections e ON e.organization_id=j.organization_id AND e.id=j.environment_id
		WHERE j.status='queued' AND j.next_attempt_at<=now() AND e.connection_status='connected'
		ORDER BY j.next_attempt_at,j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT 1`).Scan(
		&job.ID, &job.Trigger, &job.Attempts, &job.QueuedAt, &job.Environment.ID, &job.Environment.OrganizationID, &job.Environment.Provider, &job.Environment.ExternalID, &job.Environment.DisplayName, &job.Environment.SourceID, &job.Environment.TargetID, &config)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimedCloudJob{}, false, nil
	}
	if err != nil {
		return claimedCloudJob{}, false, err
	}
	job.Environment.Configuration = config
	if _, err = tx.Exec(ctx, `UPDATE cloud_scan_jobs SET status='running',phase='acquiring_credentials',started_at=COALESCE(started_at,now()),attempts=attempts+1 WHERE id=$1`, job.ID); err != nil {
		return claimedCloudJob{}, true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return claimedCloudJob{}, true, err
	}
	return job, true, nil
}

func (w CloudWorker) cancelWhenDisconnected(ctx context.Context, cancel context.CancelFunc, organizationID, environmentID string, done <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			var connected bool
			if err := w.Pool.QueryRow(ctx, `SELECT connection_status='connected' FROM environment_connections WHERE organization_id=$1 AND id=$2`, organizationID, environmentID).Scan(&connected); err != nil || !connected {
				cancel()
				return
			}
		}
	}
}

func prepareCloudSnapshot(snapshot *discovery.Snapshot, environment cloud.Environment) error {
	if snapshot.SchemaVersion == "" {
		snapshot.SchemaVersion = discovery.SchemaVersion
	}
	if snapshot.SnapshotID == "" {
		snapshot.SnapshotID = uuid.NewString()
	}
	snapshot.OrganizationID = environment.OrganizationID
	snapshot.SourceID = environment.SourceID
	snapshot.TargetID = environment.TargetID
	snapshot.SourceType = discovery.SourceCloud
	if snapshot.Collector.ID == "" {
		snapshot.Collector = discovery.Collector{ID: "lens-cloud-" + environment.Provider, Name: "Lens " + environment.Provider + " adapter", Version: Version, Mode: "managed"}
	}
	if snapshot.ObservedAt == "" {
		snapshot.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if snapshot.Scope.Name == "" {
		snapshot.Scope.Name = environment.DisplayName
	}
	if snapshot.Scope.Attributes == nil {
		snapshot.Scope.Attributes = map[string]string{}
	}
	snapshot.Scope.Attributes["platform"] = environment.Provider
	snapshot.Full = true
	return snapshot.Validate()
}

func (w CloudWorker) failOrRetry(ctx context.Context, job claimedCloudJob, scanErr error) error {
	code, message, retryable, retryAfter := cloud.SafeError(scanErr)
	attempt := job.Attempts + 1
	if retryAfter <= 0 {
		retryAfter = time.Duration(1<<min(attempt, 6)) * time.Second
	}
	status, phase := "failed", "failed"
	if retryable && attempt < 5 {
		status, phase = "queued", "retry_wait"
	}
	_, err := w.Pool.Exec(ctx, `UPDATE cloud_scan_jobs SET status=$2,phase=$3,error_code=$4,error_message=$5,next_attempt_at=CASE WHEN $2='queued' THEN now()+$6::interval ELSE next_attempt_at END,completed_at=CASE WHEN $2='failed' THEN now() ELSE NULL END WHERE id=$1`, job.ID, status, phase, code, message, fmt.Sprintf("%f seconds", retryAfter.Seconds()))
	if err != nil {
		return err
	}
	if status == "failed" {
		_, _ = w.Pool.Exec(ctx, `INSERT INTO product_events(id,organization_id,event_type,properties) VALUES($1,$2,'scan_failed',$3)`, uuid.New(), job.Environment.OrganizationID, jsonBytes(map[string]any{"provider": job.Environment.Provider, "reason": code, "queue_ms": time.Since(job.QueuedAt).Milliseconds()}))
	}
	return scanErr
}

func (w CloudWorker) finalizeOne(ctx context.Context) (bool, error) {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var jobID, environmentID uuid.UUID
	var orgID, trigger, ingestionStatus string
	var partial bool
	var createdAt time.Time
	var startedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT j.id,j.organization_id,j.environment_id,j.trigger,j.created_at,j.started_at,i.status,COALESCE(s.latest_partial,false)
		FROM cloud_scan_jobs j JOIN ingestion_jobs i ON i.id=j.ingestion_job_id
		JOIN environment_connections e ON e.organization_id=j.organization_id AND e.id=j.environment_id
		LEFT JOIN sources s ON s.organization_id=e.organization_id AND s.id=e.source_id
		WHERE j.status='ingesting' AND i.status IN ('complete','failed')
		ORDER BY j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT 1`).Scan(&jobID, &orgID, &environmentID, &trigger, &createdAt, &startedAt, &ingestionStatus, &partial)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	status, phase := "complete", "complete"
	if ingestionStatus == "failed" {
		status, phase = "failed", "normalization_failed"
	} else if partial {
		status, phase = "partial", "complete"
	}
	_, err = tx.Exec(ctx, `UPDATE cloud_scan_jobs SET status=$2,phase=$3,completed_at=now(),error_code=CASE WHEN $2='failed' THEN 'normalization_failed' ELSE error_code END,error_message=CASE WHEN $2='failed' THEN 'Lens could not normalize the provider result' ELSE error_message END WHERE id=$1`, jobID, status, phase)
	if err == nil && trigger == "first_scan" && status != "failed" {
		_, err = tx.Exec(ctx, `INSERT INTO notification_outbox(id,organization_id,event_type,payload) VALUES($1,$2,'first_scan_completed',$3) ON CONFLICT DO NOTHING`, uuid.New(), orgID, jsonBytes(map[string]any{"environment_id": environmentID.String(), "scan_id": jobID.String(), "status": status}))
	}
	if err == nil {
		started := createdAt
		if startedAt != nil {
			started = *startedAt
		}
		eventType := "scan_completed"
		if status == "failed" {
			eventType = "scan_failed"
		} else if trigger == "first_scan" {
			eventType = "first_scan_completed"
		}
		_, err = tx.Exec(ctx, `INSERT INTO product_events(id,organization_id,event_type,properties) VALUES($1,$2,$3,$4)`, uuid.New(), orgID, eventType, jsonBytes(map[string]any{"status": status, "queue_ms": started.Sub(createdAt).Milliseconds(), "duration_ms": time.Since(started).Milliseconds()}))
	}
	if err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func (w CloudWorker) enqueueScheduled(ctx context.Context) error {
	rows, err := w.Pool.Query(ctx, `SELECT id,organization_id FROM environment_connections WHERE connection_status='connected' AND schedule_enabled=true AND provider IN ('aws','azure','gcp') AND next_scan_at<=now() ORDER BY next_scan_at LIMIT 100`)
	if err != nil {
		return err
	}
	type due struct {
		id  uuid.UUID
		org string
	}
	items := []due{}
	for rows.Next() {
		var value due
		if err := rows.Scan(&value.id, &value.org); err != nil {
			rows.Close()
			return err
		}
		items = append(items, value)
	}
	rows.Close()
	for _, value := range items {
		tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO cloud_scan_jobs(id,organization_id,environment_id,trigger,status) VALUES($1,$2,$3,'scheduled','queued') ON CONFLICT DO NOTHING`, uuid.New(), value.org, value.id)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE environment_connections SET next_scan_at=$3,updated_at=now() WHERE organization_id=$1 AND id=$2`, value.org, value.id, nextDailyScan(value.id, time.Now().UTC()))
		}
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (w CloudWorker) purgeExpired(ctx context.Context) error {
	rows, err := w.Pool.Query(ctx, `SELECT id,organization_id,source_id,target_id FROM environment_connections WHERE connection_status='disconnected' AND purge_after<=now() ORDER BY purge_after LIMIT 25`)
	if err != nil {
		return err
	}
	type expired struct {
		id             uuid.UUID
		org            string
		source, target *string
	}
	items := []expired{}
	for rows.Next() {
		var value expired
		if err := rows.Scan(&value.id, &value.org, &value.source, &value.target); err != nil {
			rows.Close()
			return err
		}
		items = append(items, value)
	}
	rows.Close()
	for _, value := range items {
		tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM environment_connections WHERE organization_id=$1 AND id=$2`, value.org, value.id)
		if value.source != nil {
			if err == nil {
				_, err = tx.Exec(ctx, `DELETE FROM evidence_observations WHERE organization_id=$1 AND source_id=$2`, value.org, *value.source)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `DELETE FROM changes WHERE organization_id=$1 AND source_id=$2`, value.org, *value.source)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `DELETE FROM sources WHERE organization_id=$1 AND id=$2`, value.org, *value.source)
			}
		}
		if err == nil && value.target != nil {
			_, err = tx.Exec(ctx, `DELETE FROM discovery_targets WHERE organization_id=$1 AND id=$2`, value.org, *value.target)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM relationships r WHERE r.organization_id=$1 AND NOT EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id)`, value.org)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM entities e WHERE e.organization_id=$1 AND NOT EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id)`, value.org)
		}
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}
