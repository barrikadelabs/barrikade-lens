package hub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applyTestSnapshot(ctx context.Context, pool *pgxpool.Pool, snapshot discovery.Snapshot) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := normalizeSnapshot(ctx, tx, snapshot); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func TestRepeatSnapshotCreatesNoRefreshNoiseAndStateChangeHasDiff(t *testing.T) {
	ctx, pool := integrationPool(t)
	orgID := "changes-" + uuid.NewString()
	sourceID := "source:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'change test')`, orgID); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, orgID, sourceID, "endpoint", "endpoint.local"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, orgID) })
	entityID := discovery.StableID(orgID, discovery.KindRuntime, "target:"+sourceID+":runtime:codex")
	entity := discovery.Entity{ID: entityID, Kind: discovery.KindRuntime, CanonicalKey: "target:" + sourceID + ":runtime:codex", Name: "Codex", Attributes: map[string]any{"product_id": "codex", "product_category": "agent_tool", "configured": true, "source_surface": "endpoint"}, Confidence: discovery.ConfidenceConfirmed}
	makeSnapshot := func(sequence uint64, value discovery.Entity) discovery.Snapshot {
		snapshot := discovery.NewSnapshot(orgID, sourceID, discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "2", Mode: "managed"})
		snapshot.Sequence = sequence
		snapshot.Entities = []discovery.Entity{value}
		snapshot.Coverage.DetectorsRun = 1
		return snapshot
	}
	if err := applyTestSnapshot(ctx, pool, makeSnapshot(1, entity)); err != nil {
		t.Fatal(err)
	}
	var initialChanges int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM changes WHERE organization_id=$1`, orgID).Scan(&initialChanges); err != nil {
		t.Fatal(err)
	}
	if err := applyTestSnapshot(ctx, pool, makeSnapshot(2, entity)); err != nil {
		t.Fatal(err)
	}
	var repeatChanges int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM changes WHERE organization_id=$1`, orgID).Scan(&repeatChanges); err != nil {
		t.Fatal(err)
	}
	if repeatChanges != initialChanges {
		t.Fatalf("identical scan created %d refresh events", repeatChanges-initialChanges)
	}
	entity.Attributes["running_at_scan"] = true
	if err := applyTestSnapshot(ctx, pool, makeSnapshot(3, entity)); err != nil {
		t.Fatal(err)
	}
	var category, summary string
	var details []byte
	if err := pool.QueryRow(ctx, `SELECT category,summary,details FROM changes WHERE organization_id=$1 AND event_type='entity.updated' ORDER BY changed_at DESC LIMIT 1`, orgID).Scan(&category, &summary, &details); err != nil {
		t.Fatal(err)
	}
	if category != "state" || summary != "Configured → Running" || !json.Valid(details) {
		t.Fatalf("unexpected state change category=%q summary=%q details=%s", category, summary, details)
	}
	var postureState, systemType string
	if err := pool.QueryRow(ctx, `SELECT discovery_state,system_type FROM entity_posture WHERE organization_id=$1 AND entity_id=$2`, orgID, entityID).Scan(&postureState, &systemType); err != nil {
		t.Fatal(err)
	}
	if postureState != "running" || systemType != "agent_tool" {
		t.Fatalf("unexpected posture state=%q system_type=%q", postureState, systemType)
	}
}

func TestSourceObservationsMergeDeterministicallyAndExposeConflicts(t *testing.T) {
	ctx, pool := integrationPool(t)
	orgID := "merge-" + uuid.NewString()
	endpointSource := "endpoint:" + uuid.NewString()
	repositorySource := "repository:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'merge test')`, orgID); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, orgID, endpointSource, "endpoint", "endpoint.local"); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, orgID, repositorySource, "repository", "acme/agents"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, orgID) })
	entityID := discovery.StableID(orgID, discovery.KindModel, "shared:model")
	apply := func(source string, sourceType discovery.SourceType, sequence uint64, confidence discovery.Confidence, attributes map[string]any) error {
		snapshot := discovery.NewSnapshot(orgID, source, sourceType, discovery.Collector{ID: "test", Name: "test", Version: "2", Mode: "test"})
		snapshot.Sequence = sequence
		snapshot.Entities = []discovery.Entity{{ID: entityID, Kind: discovery.KindModel, CanonicalKey: "shared:model", Name: "Shared Model", Attributes: attributes, Confidence: confidence}}
		return applyTestSnapshot(ctx, pool, snapshot)
	}
	if err := apply(endpointSource, discovery.SourceEndpoint, 1, discovery.ConfidenceLikely, map[string]any{"cached": false, "provider": "local", "formats": []string{"gguf"}}); err != nil {
		t.Fatal(err)
	}
	if err := apply(repositorySource, discovery.SourceRepository, 1, discovery.ConfidenceConfirmed, map[string]any{"cached": true, "provider": "declared", "formats": []string{"safetensors"}}); err != nil {
		t.Fatal(err)
	}
	var attributes []byte
	var confidence string
	if err := pool.QueryRow(ctx, `SELECT attributes,confidence FROM entities WHERE organization_id=$1 AND id=$2`, orgID, entityID).Scan(&attributes, &confidence); err != nil {
		t.Fatal(err)
	}
	var merged map[string]any
	_ = json.Unmarshal(attributes, &merged)
	formats, _ := merged["formats"].([]any)
	if confidence != "confirmed" || merged["cached"] != true || merged["provider"] != "declared" || len(formats) != 2 {
		t.Fatalf("unexpected aggregate confidence=%q attributes=%v", confidence, merged)
	}
	var conflicts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM data_quality_conflicts WHERE organization_id=$1 AND entity_id=$2 AND attribute_path='attributes.provider'`, orgID, entityID).Scan(&conflicts); err != nil {
		t.Fatal(err)
	}
	if conflicts != 1 {
		t.Fatalf("expected one scalar conflict, got %d", conflicts)
	}
	// Re-observing the lower-confidence source later must not change the selected scalar.
	time.Sleep(time.Millisecond)
	if err := apply(endpointSource, discovery.SourceEndpoint, 2, discovery.ConfidenceLikely, map[string]any{"cached": false, "provider": "local", "formats": []string{"gguf"}}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT attributes FROM entities WHERE organization_id=$1 AND id=$2`, orgID, entityID).Scan(&attributes); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(attributes, &merged)
	if merged["provider"] != "declared" {
		t.Fatalf("source order changed the deterministic winner: %v", merged)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE source_entities SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2`, orgID, repositorySource); err != nil {
		t.Fatal(err)
	}
	current, err := recomputeEntityFromCurrentObservations(ctx, tx, orgID, entityID)
	if err != nil || !current {
		t.Fatalf("shared entity was not preserved after source removal: current=%v err=%v", current, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT attributes FROM entities WHERE organization_id=$1 AND id=$2`, orgID, entityID).Scan(&attributes); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(attributes, &merged)
	if merged["provider"] != "local" || merged["cached"] != false {
		t.Fatalf("removed source facts remained on the shared entity: %v", merged)
	}
}

func TestRootSystemNetworkScopeFollowsConnectedServer(t *testing.T) {
	ctx, pool := integrationPool(t)
	orgID := "network-" + uuid.NewString()
	sourceID := "source:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'network posture test')`, orgID); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, orgID, sourceID, "endpoint", "endpoint.local"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, orgID) })

	runtimeKey := "target:" + sourceID + ":runtime:ollama"
	serverKey := "target:" + sourceID + ":model-server:11434"
	runtimeID := discovery.StableID(orgID, discovery.KindRuntime, runtimeKey)
	serverID := discovery.StableID(orgID, discovery.KindModelServer, serverKey)
	relationshipID := discovery.RelationshipID(orgID, discovery.RelationshipProvides, runtimeID, serverID)
	makeSnapshot := func(sequence uint64, binding string) discovery.Snapshot {
		snapshot := discovery.NewSnapshot(orgID, sourceID, discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "2", Mode: "managed"})
		snapshot.Sequence = sequence
		snapshot.Entities = []discovery.Entity{
			{ID: runtimeID, Kind: discovery.KindRuntime, CanonicalKey: runtimeKey, Name: "Ollama", Attributes: map[string]any{"product_id": "ollama", "product_category": "model_runtime", "running_at_scan": true, "source_surface": "endpoint"}, Confidence: discovery.ConfidenceConfirmed},
			{ID: serverID, Kind: discovery.KindModelServer, CanonicalKey: serverKey, Name: "Ollama", Attributes: map[string]any{"running_at_scan": true, "transport": "http", "port": 11434, "binding": binding, "source_surface": "endpoint"}, Confidence: discovery.ConfidenceConfirmed},
		}
		snapshot.Relationships = []discovery.Relationship{{ID: relationshipID, Kind: discovery.RelationshipProvides, From: runtimeID, To: serverID, Confidence: discovery.ConfidenceConfirmed}}
		return snapshot
	}
	if err := applyTestSnapshot(ctx, pool, makeSnapshot(1, "loopback")); err != nil {
		t.Fatal(err)
	}
	var scope string
	if err := pool.QueryRow(ctx, `SELECT network_scope FROM entity_posture WHERE organization_id=$1 AND entity_id=$2`, orgID, runtimeID).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if scope != "loopback" {
		t.Fatalf("connected loopback listener did not reach the root system posture: %q", scope)
	}
	if err := applyTestSnapshot(ctx, pool, makeSnapshot(2, "all_interfaces")); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT network_scope FROM entity_posture WHERE organization_id=$1 AND entity_id=$2`, orgID, runtimeID).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if scope != "network" {
		t.Fatalf("network binding did not reach the root system posture: %q", scope)
	}
	var category, summary string
	var details []byte
	if err := pool.QueryRow(ctx, `SELECT category,summary,details FROM changes WHERE organization_id=$1 AND entity_id=$2 AND event_type='entity.updated' ORDER BY changed_at DESC LIMIT 1`, orgID, runtimeID).Scan(&category, &summary, &details); err != nil {
		t.Fatal(err)
	}
	if category != "network_scope" || summary != "Loopback → Network accessible" || !json.Valid(details) {
		t.Fatalf("connected network change was not reported on the root system: category=%q summary=%q details=%s", category, summary, details)
	}
}
