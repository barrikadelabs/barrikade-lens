package hub

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAnalyticsOutboxCommitLeasingRecoveryAndOptOut(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "analytics-" + uuid.NewString()
	actor := "clerk:user_" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'analytics test')`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO managed_users(user_id,status) VALUES($1,'active')`, actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org)
		_, _ = pool.Exec(ctx, `DELETE FROM managed_users WHERE user_id=$1`, actor)
	})
	config := ProductAnalyticsConfig{Enabled: true, ProjectToken: "phc_test", Host: "https://eu.i.posthog.com", IDSalt: []byte("01234567890123456789012345678901"), DeploymentEnvironment: "staging"}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordProductEvent(ctx, tx, config, ProductEvent{OrganizationID: org, ActorID: actor, Name: "workspace_created", DedupeKey: "rolled-back"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_events WHERE organization_id=$1`, org).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled-back analytics event was visible: %d", count)
	}

	for _, key := range []string{"first", "second"} {
		if err := recordProductEvent(ctx, pool, config, ProductEvent{OrganizationID: org, Name: "scan_received", Properties: map[string]any{"connection_type": "endpoint", "lifecycle_phase": "received"}, DedupeKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	workerA := &PostHogWorker{Pool: pool, Config: config}
	workerB := &PostHogWorker{Pool: pool, Config: config}
	first, ok, err := workerA.claimOne(ctx)
	if err != nil || !ok {
		t.Fatalf("first claim failed: ok=%v err=%v", ok, err)
	}
	second, ok, err := workerB.claimOne(ctx)
	if err != nil || !ok {
		t.Fatalf("second claim failed: ok=%v err=%v", ok, err)
	}
	if first.ID == second.ID {
		t.Fatal("two replicas claimed the same event")
	}

	if _, err := pool.Exec(ctx, `UPDATE product_events SET posthog_lease_expires_at=$2 WHERE id=$1`, first.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	recovered, ok, err := workerB.claimOne(ctx)
	if err != nil || !ok {
		t.Fatalf("expired lease was not recovered: ok=%v err=%v", ok, err)
	}
	if recovered.ID != first.ID || recovered.Attempts != first.Attempts+1 {
		t.Fatal("recovery did not retain UUID and increment attempts")
	}
	captured := workerB.capture(recovered)
	if captured.Uuid != recovered.ID || captured.DistinctId != config.WorkspaceID(org) || captured.Properties["workspace_id"] != config.WorkspaceID(org) {
		t.Fatal("captured event did not use stable UUID and workspace pseudonym")
	}

	if err := recordProductEvent(ctx, pool, config, ProductEvent{OrganizationID: org, ActorID: actor, Name: "export_generated", Properties: map[string]any{"export_format": "json"}, DedupeKey: "opted-out"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_users SET analytics_opted_out_at=now() WHERE user_id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	_, ok, err = workerA.claimOne(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("actor-attributed event was delivered after opt-out")
	}
	var status, safeError string
	if err := pool.QueryRow(ctx, `SELECT posthog_status,posthog_last_error FROM product_events WHERE organization_id=$1 AND dedupe_key='opted-out'`, org).Scan(&status, &safeError); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" || safeError != "opted_out" {
		t.Fatalf("unexpected opt-out state: %s %s", status, safeError)
	}
	if err := recordProductEvent(ctx, pool, config, ProductEvent{OrganizationID: org, ActorID: actor, Name: "export_generated", Properties: map[string]any{"export_format": "json"}, DedupeKey: "future-opted-out"}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_events WHERE organization_id=$1 AND dedupe_key='future-opted-out'`, org).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("future actor event was stored after opt-out")
	}
}
