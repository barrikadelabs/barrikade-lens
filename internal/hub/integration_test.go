package hub

import (
	"context"
	"encoding/json"
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

func TestRegistryServiceAccountIsReadOnlyAndRevocable(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "registry-service-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'Registry integration test')`, org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })

	server, err := NewServer(ctx, Config{
		Pool:                  pool,
		JWTSecret:             []byte("0123456789012345678901234567890123456789"),
		DevAdminToken:         "registry-admin",
		DefaultOrganizationID: org,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/service-accounts", strings.NewReader(`{"name":"registry-reconciler","scopes":["inventory:read"]}`))
	request.Header.Set("Authorization", "Bearer registry-admin")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create service account returned %d: %s", response.Code, response.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Token == "" {
		t.Fatalf("service account response omitted one-time credentials: %s", response.Body.String())
	}

	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if got := call(http.MethodGet, "/v1/exports?format=lens", created.Token, ""); got.Code != http.StatusOK {
		t.Fatalf("service account could not read inventory: %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodPost, "/v1/discovery/snapshots", created.Token, `{}`); got.Code != http.StatusForbidden {
		t.Fatalf("read-only service account wrote discovery data: %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodDelete, "/v1/admin/service-accounts/"+created.ID, "registry-admin", ""); got.Code != http.StatusNoContent {
		t.Fatalf("revoke service account returned %d: %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodGet, "/v1/exports?format=lens", created.Token, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("revoked service account remained usable: %d %s", got.Code, got.Body.String())
	}
}

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

func insertTestSource(ctx context.Context, pool *pgxpool.Pool, organizationID, sourceID, sourceType, name string) error {
	if _, err := pool.Exec(ctx, `INSERT INTO discovery_targets(organization_id,id,target_type,name,identity_quality) VALUES($1,$2,$3,$4,'persistent')`, organizationID, sourceID, sourceType, name); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `INSERT INTO sources(organization_id,id,target_id,source_type,name) VALUES($1,$2,$2,$3,$4)`, organizationID, sourceID, sourceType, name)
	return err
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
	if err := insertTestSource(ctx, pool, org, source, "endpoint", "fixture"); err != nil {
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
	if err := insertTestSource(ctx, pool, org, source, "repository", "acme/agents"); err != nil {
		t.Fatal(err)
	}
	_, _ = pool.Exec(ctx, `UPDATE sources SET last_seen_at=now() WHERE organization_id=$1 AND id=$2`, org, source)
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
	if err := insertTestSource(ctx, pool, orgA, "source-a", "endpoint", "Visible Endpoint"); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, orgB, "source-b", "endpoint", "Other Tenant Endpoint"); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,target_id,surface,system_role,system_type,product_id,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest)
		VALUES($1,'entity-a','source-a','endpoint','system','autonomous_agent','visible-agent','agent','defined','none',false,'likely',true,$3,$3,'a'),
		($2,'entity-b','source-b','endpoint','system','autonomous_agent','other-agent','agent','defined','none',false,'likely',true,$3,$3,'b')`, orgA, orgB, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO changes(id,organization_id,source_id,entity_id,event_type,snapshot_id,details,category,summary,changed_at)
		VALUES(gen_random_uuid(),$1,'source-a','entity-a','entity.discovered',gen_random_uuid(),'{}','identity','Visible Agent discovered',$3),
		(gen_random_uuid(),$2,'source-b','entity-b','entity.discovered',gen_random_uuid(),'{}','identity','Other Tenant Agent discovered',$3)`, orgA, orgB, now)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "admin-a", DefaultOrganizationID: orgA})
	if err != nil {
		t.Fatal(err)
	}
	adminRequest := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer admin-a")
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	for _, path := range []string{"/v1/entities", "/v1/systems", "/v1/overview?window=7d", "/v1/targets", "/v1/changes?system_role=system", "/v1/coverage", "/v1/relationships"} {
		response := adminRequest(http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "Other Tenant") || strings.Contains(response.Body.String(), "entity-b") || strings.Contains(response.Body.String(), "source-b") {
			t.Fatalf("tenant isolation failure on %s: %s", path, response.Body.String())
		}
	}
	for _, path := range []string{"/v1/systems/entity-a", "/v1/targets/source-a"} {
		if response := adminRequest(http.MethodGet, path, ""); response.Code != http.StatusOK {
			t.Fatalf("own detail %s returned %d", path, response.Code)
		}
	}
	for _, path := range []string{"/v1/systems/entity-b", "/v1/targets/source-b"} {
		if response := adminRequest(http.MethodGet, path, ""); response.Code != http.StatusNotFound {
			t.Fatalf("cross-tenant detail %s returned %d", path, response.Code)
		}
	}
	baselineBody := `{"baselines":[{"target_type":"endpoint","expected_count":12},{"target_type":"repository","expected_count":null},{"target_type":"kubernetes","expected_count":null}]}`
	if response := adminRequest(http.MethodPut, "/v1/admin/coverage/baselines", baselineBody); response.Code != http.StatusOK {
		t.Fatalf("baseline update returned %d: %s", response.Code, response.Body.String())
	}
	var baselinesA, baselinesB int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM coverage_baselines WHERE organization_id=$1),(SELECT count(*) FROM coverage_baselines WHERE organization_id=$2)`, orgA, orgB).Scan(&baselinesA, &baselinesB); err != nil {
		t.Fatal(err)
	}
	if baselinesA != 1 || baselinesB != 0 {
		t.Fatalf("coverage baseline escaped organization scope: orgA=%d orgB=%d", baselinesA, baselinesB)
	}
	collector, _, err := server.auth.issueAccessToken(orgA, "source-a", []string{"discovery:write", "jobs:read"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/entities", "/v1/systems", "/v1/overview", "/v1/targets", "/v1/changes", "/v1/coverage", "/v1/relationships", "/v1/exports?format=lens"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+collector)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("collector read %s should be forbidden, got %d", path, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/discovery/snapshots", strings.NewReader(`{"schema_version":"1.0"}`))
	request.Header.Set("Authorization", "Bearer "+collector)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired || !strings.Contains(response.Body.String(), "collector_upgrade_required") {
		t.Fatalf("schema 1.0 did not receive an explicit collector upgrade response: status=%d body=%s", response.Code, response.Body.String())
	}
}
