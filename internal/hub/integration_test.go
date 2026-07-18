package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/catalog"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("LENS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("LENS_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}

type fixtureCatalogProvider struct{}

func (fixtureCatalogProvider) ID() string          { return "fixture-catalog" }
func (fixtureCatalogProvider) DisplayName() string { return "Public API Catalog" }
func (fixtureCatalogProvider) Refresh(context.Context, catalog.State) (catalog.Index, error) {
	return catalog.Index{Entries: []catalog.Entry{{ID: "api", Name: "Fixture API", ProviderID: "example.test", Reference: "fixture"}}}, nil
}
func (fixtureCatalogProvider) Match(_ catalog.Index, host, _ string) []catalog.Match {
	if host != "api.example.test" {
		return nil
	}
	return []catalog.Match{{Entry: catalog.Entry{ID: "api", Name: "Fixture API", MatchHost: host}, Confidence: "confirmed", Exact: true, Reason: "exact host"}}
}
func (fixtureCatalogProvider) Fetch(context.Context, catalog.Entry, catalog.State) (catalog.Document, error) {
	return catalog.Document{
		API:     catalog.API{ID: "fixture-api", Name: "Fixture API", Host: "api.example.test", BaseURL: "https://api.example.test/v1"},
		OpenAPI: map[string]any{"openapi": "3.1.0", "paths": map[string]any{"/agents": map[string]any{"get": map[string]any{"operationId": "listAgents"}}}},
		Arazzo:  []map[string]any{{"arazzo": "1.1.0", "workflows": []any{map[string]any{"workflowId": "discoverAgent"}}}},
	}, nil
}

func TestCatalogEnrichmentCreatesInteroperableCapabilityGraph(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "catalog-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'catalog test')`, org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	candidate := discovery.StableID(org, discovery.KindMCPServer, "remote")
	_, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'mcp_server','Remote MCP','{"host":"api.example.test"}','confirmed','{}',true,false,now(),now())`, org, candidate)
	if err != nil {
		t.Fatal(err)
	}
	worker := CatalogWorker{Pool: pool, Provider: fixtureCatalogProvider{}}
	if err := worker.refreshAndEnrich(ctx); err != nil {
		t.Fatal(err)
	}
	var entities, relationships int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entities WHERE organization_id=$1 AND current=true AND kind IN ('api_service','api_operation','workflow')`, org).Scan(&entities); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM relationships WHERE organization_id=$1 AND current=true AND attributes->>'catalog_provider'='fixture-catalog'`, org).Scan(&relationships); err != nil {
		t.Fatal(err)
	}
	if entities != 3 || relationships != 3 {
		t.Fatalf("expected service, operation, workflow and their graph edges; got %d entities and %d relationships", entities, relationships)
	}
}

func TestFullSnapshotStaleAndRemovalLifecycle(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "test-" + uuid.NewString()
	source := "source:" + uuid.NewString()
	_, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'test')`, org)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO sources(organization_id,id,source_type,name) VALUES($1,$2,'endpoint','fixture')`, org, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	entity := discovery.Entity{ID: discovery.StableID(org, discovery.KindAgent, "agent"), Kind: discovery.KindAgent, Name: "Agent", Attributes: map[string]any{"configured": true}, Confidence: discovery.ConfidenceLikely}
	apply := func(sequence uint64, entities []discovery.Entity) error {
		snapshot := discovery.NewSnapshot(org, source, discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
		snapshot.Sequence = sequence
		snapshot.Entities = entities
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
	if err := apply(1, []discovery.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	if err := apply(2, nil); err != nil {
		t.Fatal(err)
	}
	assertState(t, ctx, pool, org, entity.ID, true, false)
	if err := apply(3, nil); err != nil {
		t.Fatal(err)
	}
	assertState(t, ctx, pool, org, entity.ID, true, true)
	if err := apply(4, nil); err != nil {
		t.Fatal(err)
	}
	assertState(t, ctx, pool, org, entity.ID, false, true)
	if err := apply(4, nil); err == nil {
		t.Fatal("expected out-of-order sequence rejection")
	}
}

func assertState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org, id string, current, stale bool) {
	t.Helper()
	var gotCurrent, gotStale bool
	if err := pool.QueryRow(ctx, `SELECT current,stale FROM entities WHERE organization_id=$1 AND id=$2`, org, id).Scan(&gotCurrent, &gotStale); err != nil {
		t.Fatal(err)
	}
	if gotCurrent != current || gotStale != stale {
		t.Fatalf("got current=%v stale=%v", gotCurrent, gotStale)
	}
}

func TestRemovingGitHubRepositoryRevokesItsDiscoverySource(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "github-remove-" + uuid.NewString()
	source := "repository:" + uuid.NewString()
	entity := "agent:" + uuid.NewString()
	installationID := time.Now().UnixNano()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'GitHub removal test')`, org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	if _, err := pool.Exec(ctx, `INSERT INTO sources(organization_id,id,source_type,name,last_seen_at) VALUES($1,$2,'repository','acme/agents',now())`, org, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO github_installations(installation_id,organization_id,account_login) VALUES($1,$2,'acme')`, installationID, org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO github_repositories(installation_id,organization_id,owner,repository,source_id) VALUES($1,$2,'acme','agents',$3)`, installationID, org, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'agent','Removed Agent','{}','likely','{}',true,false,now(),now())`, org, entity); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO source_entities(organization_id,source_id,entity_id,last_seen_at,last_seen_sequence) VALUES($1,$2,$3,now(),1)`, org, source, entity); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := removeRepositorySource(ctx, tx, installationID, "ACME", "Agents"); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertState(t, ctx, pool, org, entity, false, true)
	var revoked bool
	if err := pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM sources WHERE organization_id=$1 AND id=$2`, org, source).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("source was not revoked: revoked=%v err=%v", revoked, err)
	}
	var mappings, removals int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM github_repositories WHERE organization_id=$1 AND source_id=$2`, org, source).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM changes WHERE organization_id=$1 AND entity_id=$2 AND event_type='entity.removed'`, org, entity).Scan(&removals); err != nil {
		t.Fatal(err)
	}
	if mappings != 0 || removals != 1 {
		t.Fatalf("expected mapping deletion and one removal change; mappings=%d removals=%d", mappings, removals)
	}
}

func TestHubQueriesAreOrganizationScopedAndCollectorsCannotReadInventory(t *testing.T) {
	ctx, pool := integrationPool(t)
	orgA := "a-" + uuid.NewString()
	orgB := "b-" + uuid.NewString()
	for _, org := range []string{orgA, orgB} {
		if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,$1)`, org); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	}
	now := time.Now().UTC()
	_, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,'entity-a','agent','Visible Agent','{}','likely','{}',true,false,$3,$3),($2,'entity-b','agent','Other Tenant Agent','{}','likely','{}',true,false,$3,$3)`, orgA, orgB, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO sources(organization_id,id,source_type,name) VALUES($1,'source-a','endpoint','source')`, orgA)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "admin-a", DefaultOrganizationID: orgA})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/entities", nil)
	request.Header.Set("Authorization", "Bearer admin-a")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("unexpected status %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "Other Tenant Agent") || !strings.Contains(response.Body.String(), "Visible Agent") {
		t.Fatalf("tenant isolation failure: %s", response.Body.String())
	}
	collector, _, err := server.auth.issueAccessToken(orgA, "source-a", []string{"discovery:write", "jobs:read"})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/entities", nil)
	request.Header.Set("Authorization", "Bearer "+collector)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("collector read should be forbidden, got %d", response.Code)
	}
}
