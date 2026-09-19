package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	scaleCurrentEntities = 1_000_000
	scaleStaleEntities   = 20_000
	scaleQueryTarget     = 2 * time.Second
	scaleSelectedEntity  = "scale-0000001"
)

type scaleMeasurement struct {
	Name          string  `json:"name"`
	Path          string  `json:"path"`
	Phase         string  `json:"phase"`
	Run           int     `json:"run"`
	Milliseconds  float64 `json:"milliseconds"`
	Status        int     `json:"status"`
	ResponseBytes int     `json:"response_bytes"`
}

type scaleDiagnostics struct {
	ctx          context.Context
	pool         *pgxpool.Pool
	organization string
	directory    string
	measurements []scaleMeasurement
	ready        bool
}

func TestMillionEntityInventoryQueryUnderTwoSeconds(t *testing.T) {
	if os.Getenv("LENS_RUN_SCALE_TESTS") != "1" {
		t.Skip("set LENS_RUN_SCALE_TESTS=1 to run the million-entity gate")
	}
	ctx, pool := integrationPool(t)
	org := "scale-" + uuid.NewString()
	foreignOrg := "scale-foreign-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'scale test'),($2,'foreign scale test')`, org, foreignOrg); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LENS_SCALE_DISPOSABLE_DATABASE") != "1" {
		t.Cleanup(func() { cleanupScaleFixture(ctx, pool, org, foreignOrg) })
	}

	artifactDirectory := os.Getenv("LENS_SCALE_ARTIFACT_DIR")
	if artifactDirectory == "" {
		artifactDirectory = t.TempDir()
		t.Logf("scale diagnostics: %s", artifactDirectory)
	}
	diagnostics := &scaleDiagnostics{ctx: ctx, pool: pool, organization: org, directory: artifactDirectory}
	t.Cleanup(func() { diagnostics.write(t) })

	fixtureStarted := time.Now()
	if err := loadScaleFixture(ctx, pool, org, foreignOrg); err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded scale fixture in %s", time.Since(fixtureStarted))
	diagnostics.ready = true

	var currentEntities, staleObservations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entities WHERE organization_id=$1 AND current=true`, org).Scan(&currentEntities); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_entities WHERE organization_id=$1 AND stale=true`, org).Scan(&staleObservations); err != nil {
		t.Fatal(err)
	}
	if currentEntities != scaleCurrentEntities || staleObservations < scaleStaleEntities {
		t.Fatalf("scale fixture was weakened: current_entities=%d stale_observations=%d", currentEntities, staleObservations)
	}

	server, err := NewServer(ctx, Config{
		Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"),
		DevAdminToken: "scale-admin", DefaultOrganizationID: org, ExposureEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string) (*httptest.ResponseRecorder, time.Duration) {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer scale-admin")
		response := httptest.NewRecorder()
		started := time.Now()
		server.Handler().ServeHTTP(response, r)
		return response, time.Since(started)
	}

	cases := []struct {
		name     string
		path     string
		validate func(*testing.T, *httptest.ResponseRecorder)
	}{
		{name: "inventory-page", path: "/v1/systems?freshness=all&limit=100", validate: validateInventoryPage},
		{name: "one-hop-expansion", path: "/v1/systems/" + scaleSelectedEntity, validate: validateOneHopExpansion},
		{name: "recent-material-changes", path: "/v1/changes?window=7d&limit=100", validate: validateChangePage},
		{name: "evidence-lookup", path: "/v1/entities/" + scaleSelectedEntity, validate: validateEvidenceLookup},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			for _, phase := range []struct {
				name  string
				runs  int
				reset bool
			}{{name: "cold-connection", runs: 2, reset: true}, {name: "warm", runs: 3}} {
				for run := 1; run <= phase.runs; run++ {
					if phase.reset {
						pool.Reset()
					}
					response, elapsed := request(item.path)
					diagnostics.measurements = append(diagnostics.measurements, scaleMeasurement{
						Name: item.name, Path: item.path, Phase: phase.name, Run: run,
						Milliseconds: float64(elapsed.Microseconds()) / 1000, Status: response.Code, ResponseBytes: response.Body.Len(),
					})
					if response.Code != http.StatusOK {
						t.Errorf("%s %s run %d returned %d: %s", item.name, phase.name, run, response.Code, response.Body.String())
						continue
					}
					if strings.Contains(response.Body.String(), "FOREIGN TENANT CANARY") {
						t.Errorf("%s escaped organization isolation", item.name)
					}
					if elapsed >= scaleQueryTarget {
						t.Errorf("%s %s run %d took %s; target is %s", item.name, phase.name, run, elapsed, scaleQueryTarget)
					}
					item.validate(t, response)
				}
			}
		})
	}

	first, firstElapsed := request("/v1/systems?freshness=all&limit=100")
	firstPage := decodeScalePage(t, first)
	if firstPage.NextCursor == "" {
		t.Fatal("inventory first page omitted its seek cursor")
	}
	secondPath := "/v1/systems?freshness=all&limit=100&cursor=" + url.QueryEscape(firstPage.NextCursor)
	second, secondElapsed := request(secondPath)
	secondPage := decodeScalePage(t, second)
	repeated, repeatedElapsed := request("/v1/systems?freshness=all&limit=100")
	repeatedPage := decodeScalePage(t, repeated)
	diagnostics.measurements = append(diagnostics.measurements,
		scaleMeasurement{Name: "inventory-pagination", Path: "/v1/systems?freshness=all&limit=100", Phase: "first-page", Run: 1, Milliseconds: float64(firstElapsed.Microseconds()) / 1000, Status: first.Code, ResponseBytes: first.Body.Len()},
		scaleMeasurement{Name: "inventory-pagination", Path: secondPath, Phase: "second-page", Run: 1, Milliseconds: float64(secondElapsed.Microseconds()) / 1000, Status: second.Code, ResponseBytes: second.Body.Len()},
		scaleMeasurement{Name: "inventory-pagination", Path: "/v1/systems?freshness=all&limit=100", Phase: "stable-repeat", Run: 1, Milliseconds: float64(repeatedElapsed.Microseconds()) / 1000, Status: repeated.Code, ResponseBytes: repeated.Body.Len()},
	)
	if firstElapsed >= scaleQueryTarget || secondElapsed >= scaleQueryTarget || repeatedElapsed >= scaleQueryTarget {
		t.Errorf("seek pagination exceeded %s: first=%s second=%s repeated=%s", scaleQueryTarget, firstElapsed, secondElapsed, repeatedElapsed)
	}
	seen := map[string]bool{}
	for _, item := range firstPage.Items {
		seen[item.ID] = true
	}
	for _, item := range secondPage.Items {
		if seen[item.ID] {
			t.Errorf("seek pagination repeated entity %q across adjacent pages", item.ID)
		}
	}
	pageIDs := func(page scalePage) []string {
		ids := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		return ids
	}
	if firstPage.NextCursor != repeatedPage.NextCursor || !slices.Equal(pageIDs(firstPage), pageIDs(repeatedPage)) {
		t.Error("inventory seek page changed without an intervening write")
	}
	if len(firstPage.Items) != 100 || len(secondPage.Items) != 100 || first.Body.Len() > 1<<20 || second.Body.Len() > 1<<20 {
		t.Errorf("inventory pagination was not bounded: first_items=%d second_items=%d first_bytes=%d second_bytes=%d", len(firstPage.Items), len(secondPage.Items), first.Body.Len(), second.Body.Len())
	}

	for _, path := range []string{"/v1/entities/foreign-only", "/v1/systems/foreign-only"} {
		response, _ := request(path)
		if response.Code != http.StatusNotFound {
			t.Errorf("cross-organization detail %s returned %d", path, response.Code)
		}
	}
}

func loadScaleFixture(ctx context.Context, pool *pgxpool.Pool, org, foreignOrg string) error {
	sources := []struct{ id, sourceType, name string }{
		{"scale-source-endpoint-a", "endpoint", "Scale endpoint A"},
		{"scale-source-endpoint-b", "endpoint", "Scale endpoint B"},
		{"scale-source-repository", "repository", "Scale repository"},
		{"scale-source-kubernetes", "kubernetes", "Scale cluster"},
	}
	for _, source := range sources {
		if err := insertTestSource(ctx, pool, org, source.id, source.sourceType, source.name); err != nil {
			return err
		}
	}
	if err := insertTestSource(ctx, pool, foreignOrg, "foreign-source", "endpoint", "FOREIGN TENANT CANARY"); err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := pool.Exec(ctx, `UPDATE discovery_targets SET last_seen_at=CASE WHEN id='scale-source-kubernetes' THEN $2::timestamptz-interval '2 days' ELSE $2::timestamptz END,last_full_at=$2::timestamptz WHERE organization_id=$1`, org, now); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `UPDATE sources SET last_seen_at=CASE WHEN id='scale-source-kubernetes' THEN $2::timestamptz-interval '2 days' ELSE $2::timestamptz END,last_full_at=$2::timestamptz WHERE organization_id=$1`, org, now); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `UPDATE discovery_targets SET last_seen_at=$2::timestamptz,last_full_at=$2::timestamptz WHERE organization_id=$1`, foreignOrg, now.Add(time.Hour)); err != nil {
		return fmt.Errorf("update foreign target fixture: %w", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sources SET last_seen_at=$2::timestamptz,last_full_at=$2::timestamptz WHERE organization_id=$1`, foreignOrg, now.Add(time.Hour)); err != nil {
		return fmt.Errorf("update foreign source fixture: %w", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at)
		SELECT $1,'scale-'||lpad(value::text,7,'0'),CASE value%4 WHEN 0 THEN 'agent' WHEN 1 THEN 'runtime' WHEN 2 THEN 'mcp_server' ELSE 'model_server' END,
			'runtime:'||value,'Runtime '||value,jsonb_build_object('installed',true,'fixture_bucket',value%32),
			CASE value%3 WHEN 0 THEN 'confirmed' WHEN 1 THEN 'likely' ELSE 'possible' END,'{}',true,false,$2::timestamptz,$2::timestamptz-(value%3600)*interval '1 second'
		FROM generate_series(1,$3) value`, org, now, scaleCurrentEntities); err != nil {
		return fmt.Errorf("insert current entities: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at)
		SELECT $1,'stale-'||lpad(value::text,7,'0'),'runtime','stale:'||value,'Stale Runtime '||value,'{"installed":true}'::jsonb,'likely','{}',false,true,$2::timestamptz-interval '30 days',$2::timestamptz-interval '10 days'
		FROM generate_series(1,$3) value`, org, now, scaleStaleEntities); err != nil {
		return fmt.Errorf("insert stale entities: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,target_id,surface,system_role,system_type,product_id,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest)
		SELECT organization_id,id,
			CASE substring(id from 7)::integer%4 WHEN 0 THEN 'scale-source-endpoint-a' WHEN 1 THEN 'scale-source-endpoint-b' WHEN 2 THEN 'scale-source-repository' ELSE 'scale-source-kubernetes' END,
			CASE substring(id from 7)::integer%4 WHEN 2 THEN 'repository' WHEN 3 THEN 'kubernetes' ELSE 'endpoint' END,
			'system',CASE substring(id from 7)::integer%3 WHEN 0 THEN 'autonomous_agent' WHEN 1 THEN 'agent_tool' ELSE 'model_runtime' END,
			'product-'||(substring(id from 7)::integer%128),'agent_tool',CASE substring(id from 7)::integer%5 WHEN 0 THEN 'running' WHEN 1 THEN 'deployed' WHEN 2 THEN 'configured' WHEN 3 THEN 'installed' ELSE 'defined' END,
			CASE substring(id from 7)::integer%10 WHEN 0 THEN 'network' ELSE 'none' END,(substring(id from 7)::integer%7)=0,confidence,true,first_seen_at,last_seen_at,id
		FROM entities WHERE organization_id=$1 AND current=true`, org); err != nil {
		return fmt.Errorf("insert current posture: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,target_id,surface,system_role,system_type,product_id,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest)
		SELECT organization_id,id,'scale-source-kubernetes','kubernetes','system','agent_tool','stale-product','agent_tool','installed','none',false,confidence,false,first_seen_at,last_seen_at,id
		FROM entities WHERE organization_id=$1 AND current=false`, org); err != nil {
		return fmt.Errorf("insert stale posture: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO source_entities(organization_id,source_id,entity_id,last_seen_at,last_seen_sequence,consecutive_full_misses,current,stale,observation_name,observation_kind,canonical_key,attributes,confidence,provenance,material_digest)
		SELECT $1,CASE value%4 WHEN 0 THEN 'scale-source-endpoint-a' WHEN 1 THEN 'scale-source-endpoint-b' WHEN 2 THEN 'scale-source-repository' ELSE 'scale-source-kubernetes' END,
			'scale-'||lpad(value::text,7,'0'),$2::timestamptz,1,0,true,false,'Runtime '||value,'runtime','runtime:'||value,'{"installed":true}'::jsonb,'likely','{}','current-'||value
		FROM generate_series(1,50000) value`, org, now); err != nil {
		return fmt.Errorf("insert current observations: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO source_entities(organization_id,source_id,entity_id,last_seen_at,last_seen_sequence,consecutive_full_misses,current,stale,observation_name,observation_kind,canonical_key,attributes,confidence,provenance,material_digest)
		SELECT $1,'scale-source-kubernetes','stale-'||lpad(value::text,7,'0'),$2::timestamptz-interval '10 days',1,3,false,true,'Stale Runtime '||value,'runtime','stale:'||value,'{"installed":true}'::jsonb,'likely','{}','stale-'||value
		FROM generate_series(1,$3) value`, org, now, scaleStaleEntities); err != nil {
		return fmt.Errorf("insert stale observations: %w", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO entity_context(organization_id,entity_id,owner_name,owner_type,updated_by) SELECT $1,'scale-'||lpad(value::text,7,'0'),'Platform Security','team','scale' FROM generate_series(1,500) value`, org); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO exposure_findings(organization_id,id,root_entity_id,rule_id,rule_version,severity,title,explanation,recommended_next_step,path,evidence_bases,current,first_seen_at,last_seen_at)
		SELECT $1,'scale-finding-'||value,'scale-'||lpad(value::text,7,'0'),'scale.rule','1',CASE value%4 WHEN 0 THEN 'critical' WHEN 1 THEN 'high' WHEN 2 THEN 'medium' ELSE 'low' END,'Scale finding '||value,'Representative scale finding','Review the system','[]','{}',true,$2::timestamptz,$2::timestamptz FROM generate_series(1,10000) value`, org, now); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at)
		SELECT $1,'scale-edge-'||value,'connected_to','scale-'||lpad(value::text,7,'0'),'scale-'||lpad((value+1)::text,7,'0'),'{}','likely',true,false,$2::timestamptz,$2::timestamptz-(value%3600)*interval '1 second' FROM generate_series(1,100000) value`, org, now); err != nil {
		return fmt.Errorf("insert representative relationships: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at)
		SELECT $1,'scale-fanout-'||value,CASE value%3 WHEN 0 THEN 'uses' WHEN 1 THEN 'connects_to' ELSE 'runs_on' END,$3,'scale-'||lpad((value+1)::text,7,'0'),jsonb_build_object('fanout',value),'confirmed',true,false,$2::timestamptz,$2::timestamptz FROM generate_series(1,300) value`, org, now, scaleSelectedEntity); err != nil {
		return fmt.Errorf("insert relationship fanout: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO changes(id,organization_id,source_id,entity_id,event_type,changed_at,snapshot_id,details,category,summary)
		SELECT gen_random_uuid(),$1,CASE value%4 WHEN 0 THEN 'scale-source-endpoint-a' WHEN 1 THEN 'scale-source-endpoint-b' WHEN 2 THEN 'scale-source-repository' ELSE 'scale-source-kubernetes' END,
			'scale-'||lpad(value::text,7,'0'),'entity.updated',$2::timestamptz-(value%604800)*interval '1 second',gen_random_uuid(),jsonb_build_object('fixture',value),'state','Material state changed' FROM generate_series(1,50000) value`, org, now); err != nil {
		return fmt.Errorf("insert material changes: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence_observations(organization_id,snapshot_id,evidence_id,source_id,entity_ids,relationship_ids,detector_id,detector_version,method,family,specificity,locator,content_hash,observed_at,expires_at)
		SELECT $1,gen_random_uuid(),'scale-evidence-'||value,CASE value%4 WHEN 0 THEN 'scale-source-endpoint-a' WHEN 1 THEN 'scale-source-endpoint-b' WHEN 2 THEN 'scale-source-repository' ELSE 'scale-source-kubernetes' END,
			ARRAY['scale-'||lpad(value::text,7,'0')],ARRAY[]::text[],'scale.inventory','1','package','installation','high','fixture:'||value,'sha256:fixture',$2::timestamptz-(value%86400)*interval '1 second',$2::timestamptz+interval '90 days' FROM generate_series(1,50000) value`, org, now); err != nil {
		return fmt.Errorf("insert representative evidence: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence_observations(organization_id,snapshot_id,evidence_id,source_id,entity_ids,relationship_ids,detector_id,detector_version,method,family,specificity,locator,content_hash,observed_at,expires_at)
		SELECT $1,gen_random_uuid(),'scale-selected-evidence-'||value,'scale-source-endpoint-a',ARRAY[$3,'scale-'||lpad((value+1)::text,7,'0')],ARRAY[]::text[],
			'scale.selected','1','config_shape','configuration','high','fixture:selected','sha256:selected',$2::timestamptz-value*interval '1 second',$2::timestamptz+interval '90 days' FROM generate_series(1,200) value`, org, now, scaleSelectedEntity); err != nil {
		return fmt.Errorf("insert selected evidence history: %w", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at)
		SELECT $1,'scale-'||lpad(value::text,7,'0'),'runtime','foreign:'||value,'FOREIGN TENANT CANARY '||value,'{}','confirmed','{}',true,false,$2::timestamptz,$2::timestamptz+interval '1 hour' FROM generate_series(1,1000) value`, foreignOrg, now); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,'foreign-only','runtime','foreign-only','FOREIGN TENANT CANARY ONLY','{}','confirmed','{}',true,false,$2::timestamptz,$2::timestamptz)`, foreignOrg, now); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,target_id,surface,system_role,system_type,product_id,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest)
		SELECT organization_id,id,'foreign-source','endpoint','system','agent_tool','foreign-product','agent_tool','installed','none',false,confidence,true,first_seen_at,last_seen_at,id FROM entities WHERE organization_id=$1`, foreignOrg); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at) VALUES($1,'foreign-edge','connected_to',$3,'scale-0000002','{"tenant":"FOREIGN TENANT CANARY"}','confirmed',true,false,$2::timestamptz,$2::timestamptz)`, foreignOrg, now, scaleSelectedEntity); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO changes(id,organization_id,source_id,entity_id,event_type,changed_at,snapshot_id,details,category,summary) VALUES(gen_random_uuid(),$1,'foreign-source',$3,'entity.updated',$2::timestamptz+interval '1 hour',gen_random_uuid(),'{"tenant":"FOREIGN TENANT CANARY"}','state','FOREIGN TENANT CANARY')`, foreignOrg, now, scaleSelectedEntity); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence_observations(organization_id,snapshot_id,evidence_id,source_id,entity_ids,relationship_ids,detector_id,detector_version,method,family,specificity,locator,content_hash,observed_at,expires_at) VALUES($1,gen_random_uuid(),'foreign-evidence','foreign-source',ARRAY[$3],ARRAY[]::text[],'foreign','1','package','installation','high','FOREIGN TENANT CANARY','sha256:foreign',$2::timestamptz,$2::timestamptz+interval '90 days')`, foreignOrg, now, scaleSelectedEntity); err != nil {
		return err
	}

	for _, table := range []string{"entities", "entity_posture", "source_entities", "relationships", "changes", "evidence_observations", "exposure_findings", "entity_context"} {
		if _, err := pool.Exec(ctx, "ANALYZE "+table); err != nil {
			return fmt.Errorf("analyze %s: %w", table, err)
		}
	}
	return nil
}

func cleanupScaleFixture(ctx context.Context, pool *pgxpool.Pool, organizations ...string) {
	// Delete the high-cardinality children explicitly. A single organization
	// cascade makes PostgreSQL revisit the same million-row foreign-key graph
	// through several parents and can take longer than the benchmark itself.
	for _, table := range []string{
		"evidence_observations", "changes", "exposure_findings",
		"entity_context_history", "entity_context", "catalog_link_overrides", "data_quality_conflicts",
		"source_relationships", "relationships", "source_entities", "entity_posture", "entities",
	} {
		_, _ = pool.Exec(ctx, "DELETE FROM "+table+" WHERE organization_id=ANY($1)", organizations)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=ANY($1)`, organizations)
}

type scalePage struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func decodeScalePage(t *testing.T, response *httptest.ResponseRecorder) scalePage {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("page returned %d: %s", response.Code, response.Body.String())
	}
	var page scalePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func validateInventoryPage(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	page := decodeScalePage(t, response)
	if len(page.Items) != 100 || page.NextCursor == "" || response.Body.Len() > 1<<20 {
		t.Errorf("inventory page is unbounded or incomplete: items=%d cursor=%q bytes=%d", len(page.Items), page.NextCursor, response.Body.Len())
	}
}

func validateOneHopExpansion(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		ID          string            `json:"id"`
		Connections []json.RawMessage `json:"connections"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != scaleSelectedEntity || len(body.Connections) < 300 || len(body.Connections) > 500 {
		t.Errorf("one-hop expansion was incomplete or unbounded: entity=%q connections=%d", body.ID, len(body.Connections))
	}
}

func validateChangePage(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	page := decodeScalePage(t, response)
	if len(page.Items) != 100 || page.NextCursor == "" {
		t.Errorf("recent change page contract failed: items=%d cursor=%q", len(page.Items), page.NextCursor)
	}
}

func validateEvidenceLookup(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		ID       string            `json:"id"`
		Evidence []json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != scaleSelectedEntity || len(body.Evidence) == 0 || len(body.Evidence) > 500 {
		t.Errorf("evidence lookup was incomplete or unbounded: entity=%q evidence=%d", body.ID, len(body.Evidence))
	}
}

func (d *scaleDiagnostics) write(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(d.directory, 0o755); err != nil {
		t.Logf("create scale artifact directory: %v", err)
		return
	}
	encoded, _ := json.MarshalIndent(map[string]any{
		"generated_at": time.Now().UTC(), "organization_id": d.organization,
		"current_entity_target": scaleCurrentEntities, "query_target_ms": scaleQueryTarget.Milliseconds(),
		"measurements": d.measurements,
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(d.directory, "measurements.json"), encoded, 0o644); err != nil {
		t.Logf("write scale measurements: %v", err)
	}
	if !d.ready {
		return
	}
	d.writeQueryArtifact(t, "cardinality.txt", `SELECT 'entities.current='||count(*) FROM entities WHERE organization_id=$1 AND current=true
		UNION ALL SELECT 'entities.stale='||count(*) FROM entities WHERE organization_id=$1 AND stale=true
		UNION ALL SELECT 'posture.current='||count(*) FROM entity_posture WHERE organization_id=$1 AND current=true
		UNION ALL SELECT 'relationships.current='||count(*) FROM relationships WHERE organization_id=$1 AND current=true
		UNION ALL SELECT 'changes='||count(*) FROM changes WHERE organization_id=$1
		UNION ALL SELECT 'evidence_observations='||count(*) FROM evidence_observations WHERE organization_id=$1
		UNION ALL SELECT 'sources='||count(*) FROM sources WHERE organization_id=$1`, d.organization)
	d.writeQueryArtifact(t, "postgres-statistics.txt", `SELECT relname||' live='||n_live_tup||' dead='||n_dead_tup||' analyzed='||COALESCE(last_analyze::text,'never') FROM pg_stat_user_tables WHERE relname=ANY($1) ORDER BY relname`, []string{"entities", "entity_posture", "relationships", "changes", "evidence_observations"})

	plans := []struct {
		name, query string
		args        []any
	}{
		{name: "inventory-page-plan.txt", query: `EXPLAIN (ANALYZE,BUFFERS,VERBOSE,FORMAT TEXT)
			SELECT e.id,e.kind,e.name,p.last_seen_at FROM entity_posture p JOIN entities e ON e.organization_id=p.organization_id AND e.id=p.entity_id
			WHERE p.organization_id=$1 AND p.current=true AND p.system_role='system' ORDER BY p.last_seen_at DESC,p.entity_id DESC LIMIT 100`, args: []any{d.organization}},
		{name: "one-hop-plan.txt", query: `EXPLAIN (ANALYZE,BUFFERS,VERBOSE,FORMAT TEXT)
			SELECT r.id,r.kind,r.from_entity,r.to_entity,e.id,e.kind,e.name FROM relationships r
			JOIN entities e ON e.organization_id=r.organization_id AND e.id=CASE WHEN r.from_entity=$2 THEN r.to_entity ELSE r.from_entity END
			WHERE r.organization_id=$1 AND r.current=true AND (r.from_entity=$2 OR r.to_entity=$2) ORDER BY r.kind,e.name LIMIT 500`, args: []any{d.organization, scaleSelectedEntity}},
		{name: "recent-changes-plan.txt", query: `EXPLAIN (ANALYZE,BUFFERS,VERBOSE,FORMAT TEXT)
			SELECT c.id,c.entity_id,c.changed_at,e.name,ep.system_type FROM changes c
			LEFT JOIN entities e ON e.organization_id=c.organization_id AND e.id=c.entity_id
			LEFT JOIN entity_posture ep ON ep.organization_id=c.organization_id AND ep.entity_id=c.entity_id
			WHERE c.organization_id=$1 AND c.changed_at>=$2 ORDER BY c.changed_at DESC,c.id DESC LIMIT 100`, args: []any{d.organization, time.Now().UTC().Add(-7 * 24 * time.Hour)}},
		{name: "evidence-lookup-plan.txt", query: `EXPLAIN (ANALYZE,BUFFERS,VERBOSE,FORMAT TEXT)
			WITH observations AS (SELECT eo.evidence_id,eo.source_id,eo.detector_id,eo.method,eo.family,eo.locator,eo.entity_ids,max(eo.observed_at) observed_at,
			row_number() OVER (PARTITION BY eo.source_id,eo.detector_id,eo.method,eo.family,COALESCE(eo.locator,'') ORDER BY max(eo.observed_at) DESC,eo.evidence_id DESC) version_rank
			FROM evidence_observations eo WHERE eo.organization_id=$1 AND eo.entity_ids @> ARRAY[$2]::text[]
			GROUP BY eo.evidence_id,eo.source_id,eo.detector_id,eo.method,eo.family,eo.locator,eo.entity_ids)
			SELECT * FROM observations WHERE version_rank=1 ORDER BY observed_at DESC,evidence_id DESC LIMIT 250`, args: []any{d.organization, scaleSelectedEntity}},
	}
	for _, plan := range plans {
		d.writeQueryArtifact(t, plan.name, plan.query, plan.args...)
	}
}

func (d *scaleDiagnostics) writeQueryArtifact(t *testing.T, name, query string, args ...any) {
	t.Helper()
	rows, err := d.pool.Query(d.ctx, query, args...)
	if err != nil {
		_ = os.WriteFile(filepath.Join(d.directory, name), []byte("query failed: "+err.Error()+"\n"), 0o644)
		return
	}
	defer rows.Close()
	lines := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			lines = append(lines, "scan failed: "+err.Error())
			break
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		lines = append(lines, "rows failed: "+err.Error())
	}
	if err := os.WriteFile(filepath.Join(d.directory, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Logf("write %s: %v", name, err)
	}
}
