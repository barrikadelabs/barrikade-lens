package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	posthog "github.com/posthog/posthog-go"
)

type postHogResult struct {
	id  string
	err error
}

type postHogCallback struct{ results chan<- postHogResult }

func postHogMessageID(message posthog.APIMessage) string {
	switch typed := message.(type) {
	case posthog.CaptureInApi:
		return typed.Uuid
	case *posthog.CaptureInApi:
		return typed.Uuid
	default:
		return ""
	}
}

func (c postHogCallback) Success(message posthog.APIMessage) {
	select {
	case c.results <- postHogResult{id: postHogMessageID(message)}:
	default:
	}
}
func (c postHogCallback) Failure(message posthog.APIMessage, err error) {
	select {
	case c.results <- postHogResult{id: postHogMessageID(message), err: err}:
	default:
	}
}

type PostHogWorker struct {
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	Config       ProductAnalyticsConfig
	PollInterval time.Duration
	successes    atomic.Uint64
	retries      atomic.Uint64
	deadLetters  atomic.Uint64
}

type claimedProductEvent struct {
	ID             string
	OrganizationID string
	ActorID        string
	Name           string
	Properties     map[string]any
	CreatedAt      time.Time
	Attempts       int
}

func (w *PostHogWorker) Run(ctx context.Context) error {
	if w.Pool == nil {
		return fmt.Errorf("database pool is required")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.PollInterval <= 0 {
		w.PollInterval = 500 * time.Millisecond
	}
	results := make(chan postHogResult, 100)
	zeroRetries := 0
	client, err := posthog.NewWithConfig(w.Config.ProjectToken, posthog.Config{
		Endpoint: w.Config.Host, CaptureMode: posthog.CaptureModeAnalyticsV1,
		Callback: postHogCallback{results: results}, BatchSize: 1, Interval: 100 * time.Millisecond,
		MaxRetries: &zeroRetries, DisableGeoIP: posthog.Ptr(true), ShutdownTimeout: 5 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create PostHog client: %w", err)
	}
	defer client.Close()
	poll := time.NewTicker(w.PollInterval)
	defer poll.Stop()
	metrics := time.NewTicker(time.Minute)
	defer metrics.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-metrics.C:
			w.logQueueMetrics(ctx)
		case <-poll.C:
			event, ok, err := w.claimOne(ctx)
			if err != nil {
				w.Logger.Warn("PostHog event claim failed", "error", err)
				continue
			}
			if !ok {
				continue
			}
			if err := client.Enqueue(w.capture(event)); err != nil {
				w.finish(ctx, event, "enqueue_failed", err)
				continue
			}
			timer := time.NewTimer(20 * time.Second)
			for {
				select {
				case result := <-results:
					if result.id != event.ID {
						continue
					}
					if !timer.Stop() {
						<-timer.C
					}
					w.finish(ctx, event, "delivery_failed", result.err)
					goto delivered
				case <-timer.C:
					w.finish(ctx, event, "callback_timeout", errors.New("PostHog callback timed out"))
					goto delivered
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				}
			}
		delivered:
		}
	}
}

func (w *PostHogWorker) claimOne(ctx context.Context) (claimedProductEvent, bool, error) {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return claimedProductEvent{}, false, err
	}
	defer tx.Rollback(ctx)
	dead, err := tx.Exec(ctx, `UPDATE product_events SET posthog_status='dead',posthog_lease_expires_at=NULL,posthog_last_error='worker_interrupted'
		WHERE posthog_status='processing' AND posthog_lease_expires_at<now() AND posthog_attempts>=10`)
	if err != nil {
		return claimedProductEvent{}, false, err
	}
	if dead.RowsAffected() > 0 {
		w.deadLetters.Add(uint64(dead.RowsAffected()))
	}
	var event claimedProductEvent
	var properties []byte
	var organizationID, actorID *string
	err = tx.QueryRow(ctx, `SELECT id::text,organization_id,actor_id,event_type,properties,created_at,posthog_attempts
		FROM product_events
		WHERE posthog_attempts<10 AND ((posthog_status IN ('pending','retry') AND COALESCE(posthog_next_attempt_at,now())<=now())
		   OR (posthog_status='processing' AND posthog_lease_expires_at<now()))
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&event.ID, &organizationID, &actorID, &event.Name, &properties, &event.CreatedAt, &event.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimedProductEvent{}, false, nil
	}
	if err != nil {
		return claimedProductEvent{}, false, err
	}
	if organizationID != nil {
		event.OrganizationID = *organizationID
	}
	if actorID != nil {
		event.ActorID = *actorID
	}
	if event.ActorID != "" {
		var optedOut bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM managed_users WHERE user_id=$1 AND analytics_opted_out_at IS NOT NULL)`, event.ActorID).Scan(&optedOut); err != nil {
			return claimedProductEvent{}, false, err
		}
		if optedOut {
			_, err = tx.Exec(ctx, `UPDATE product_events SET posthog_status='delivered',posthog_delivered_at=now(),posthog_lease_expires_at=NULL,posthog_last_error='opted_out' WHERE id=$1`, event.ID)
			if err == nil {
				err = tx.Commit(ctx)
			}
			return claimedProductEvent{}, false, err
		}
	}
	if err = json.Unmarshal(properties, &event.Properties); err != nil {
		return claimedProductEvent{}, false, err
	}
	event.Attempts++
	_, err = tx.Exec(ctx, `UPDATE product_events SET posthog_status='processing',posthog_attempts=$2,posthog_lease_expires_at=now()+interval '1 minute',posthog_last_error=NULL WHERE id=$1`, event.ID, event.Attempts)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return event, err == nil, err
}

func (w *PostHogWorker) capture(event claimedProductEvent) posthog.Capture {
	properties := posthog.Properties{}
	for key, value := range event.Properties {
		properties[key] = value
	}
	properties["schema_version"] = analyticsSchemaVersion
	properties["origin"] = "backend"
	properties["app_version"] = Version
	properties["deployment_environment"] = w.Config.DeploymentEnvironment
	properties["$process_person_profile"] = false
	properties["$geoip_disable"] = true
	distinctID := ""
	if event.ActorID != "" {
		distinctID = w.Config.UserID(event.ActorID)
	}
	if event.OrganizationID != "" {
		workspaceID := w.Config.WorkspaceID(event.OrganizationID)
		properties["workspace_id"] = workspaceID
		if distinctID == "" {
			distinctID = workspaceID
		}
	}
	return posthog.Capture{Uuid: event.ID, DistinctId: distinctID, Event: event.Name, Timestamp: event.CreatedAt, Properties: properties}
}

func (w *PostHogWorker) finish(ctx context.Context, event claimedProductEvent, safeCode string, deliveryErr error) {
	if deliveryErr == nil {
		if _, err := w.Pool.Exec(ctx, `UPDATE product_events SET posthog_status='delivered',posthog_delivered_at=now(),posthog_lease_expires_at=NULL,posthog_last_error=NULL WHERE id=$1`, event.ID); err != nil {
			w.Logger.Warn("PostHog delivery acknowledgement could not be stored", "event_id", event.ID, "error", err)
			return
		}
		w.successes.Add(1)
		return
	}
	if event.Attempts >= 10 {
		_, _ = w.Pool.Exec(ctx, `UPDATE product_events SET posthog_status='dead',posthog_lease_expires_at=NULL,posthog_last_error=$2 WHERE id=$1`, event.ID, safeCode)
		w.deadLetters.Add(1)
		w.Logger.Error("PostHog event dead-lettered", "event_id", event.ID, "event", event.Name, "attempts", event.Attempts, "failure", safeCode)
		return
	}
	delay := time.Duration(1<<min(event.Attempts, 8)) * time.Second
	_, _ = w.Pool.Exec(ctx, `UPDATE product_events SET posthog_status='retry',posthog_next_attempt_at=now()+$2::interval,posthog_lease_expires_at=NULL,posthog_last_error=$3 WHERE id=$1`, event.ID, fmt.Sprintf("%f seconds", delay.Seconds()), safeCode)
	w.retries.Add(1)
}

func (w *PostHogWorker) logQueueMetrics(ctx context.Context) {
	var depth int64
	var oldest *time.Time
	if err := w.Pool.QueryRow(ctx, `SELECT count(*),min(created_at) FROM product_events WHERE posthog_status IN ('pending','retry','processing')`).Scan(&depth, &oldest); err != nil {
		w.Logger.Warn("PostHog queue metrics failed", "error", err)
		return
	}
	oldestAge := int64(0)
	if oldest != nil {
		oldestAge = time.Since(*oldest).Milliseconds()
	}
	w.Logger.Info("PostHog delivery queue", "depth", depth, "oldest_pending_ms", oldestAge, "successes", w.successes.Load(), "retries", w.retries.Load(), "dead_letters", w.deadLetters.Load())
}
