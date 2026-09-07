package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
)

func TestMillionEntityInventoryQueryUnderTwoSeconds(t *testing.T) {
	if os.Getenv("LENS_RUN_SCALE_TESTS") != "1" {
		t.Skip("set LENS_RUN_SCALE_TESTS=1 to run the million-entity gate")
	}
	ctx, pool := integrationPool(t)
	org := "scale-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'scale test')`, org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	source := "scale-source-" + uuid.NewString()
	if err := insertTestSource(ctx, pool, org, source, "endpoint", "Scale endpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE discovery_targets SET last_seen_at=now(),last_full_at=now() WHERE organization_id=$1 AND id=$2; UPDATE sources SET last_seen_at=now(),last_full_at=now() WHERE organization_id=$1 AND id=$2`, org, source); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) SELECT $1,'scale-'||value,'runtime','runtime:'||value,'Runtime '||value,'{"installed":true}'::jsonb,'likely','{}',true,false,now(),now() FROM generate_series(1,1000000) value`, org)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,target_id,surface,system_role,system_type,product_id,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest) SELECT organization_id,id,$2,'endpoint','system','agent_tool','runtime-'||id,'agent_tool','installed','none',false,confidence,true,first_seen_at,last_seen_at,id FROM entities WHERE organization_id=$1`, org, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO entity_context(organization_id,entity_id,owner_name,owner_type,updated_by) SELECT $1,'scale-'||value,'Platform Security','team','scale' FROM generate_series(1,500) value;
		INSERT INTO exposure_findings(organization_id,id,root_entity_id,rule_id,rule_version,severity,title,explanation,recommended_next_step,path,evidence_bases,current,first_seen_at,last_seen_at)
		SELECT $1,'scale-finding-'||value,'scale-'||value,'scale.rule','1',CASE value%4 WHEN 0 THEN 'critical' WHEN 1 THEN 'high' WHEN 2 THEN 'medium' ELSE 'low' END,'Scale finding '||value,'Representative scale finding','Review the system','[]','{}',true,now(),now() FROM generate_series(1,10000) value;
		INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at)
		SELECT $1,'scale-edge-'||value,'connected_to','scale-'||value,'scale-'||(value+1),'{}','likely',true,false,now(),now() FROM generate_series(1,10000) value`, org); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "scale-admin", DefaultOrganizationID: org, ExposureEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	query := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer scale-admin")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	paths := []string{"/v1/overview?window=7d", "/v1/systems?freshness=all&limit=100", "/v1/entities?limit=100", "/v1/exposures?limit=100"}
	for _, path := range paths {
		if response := query(path); response.Code != 200 {
			t.Fatalf("warmup %s returned %d", path, response.Code)
		}
		started := time.Now()
		response := query(path)
		if response.Code != 200 {
			t.Fatalf("%s returned %d", path, response.Code)
		}
		if elapsed := time.Since(started); elapsed >= 2*time.Second {
			t.Fatalf("million-entity query %s took %s", path, elapsed)
		}
	}
	overview := query("/v1/overview?window=7d")
	var overviewBody struct {
		Executive struct {
			Systems struct {
				Known int `json:"known"`
			} `json:"systems"`
			Findings struct {
				Fresh int `json:"fresh"`
			} `json:"findings"`
			Ownership struct {
				Owned int `json:"owned"`
			} `json:"effective_ownership"`
		} `json:"executive_summary"`
	}
	if err := json.Unmarshal(overview.Body.Bytes(), &overviewBody); err != nil {
		t.Fatal(err)
	}
	if overviewBody.Executive.Systems.Known != 1000000 || overviewBody.Executive.Findings.Fresh != 10000 || overviewBody.Executive.Ownership.Owned != 500 {
		t.Fatalf("representative business counts disagree: %+v", overviewBody.Executive)
	}
	page := query("/v1/systems?freshness=all&limit=100")
	var pageBody struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor string            `json:"next_cursor"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &pageBody); err != nil || len(pageBody.Items) != 100 || pageBody.NextCursor == "" {
		t.Fatalf("pagination contract failed: items=%d cursor=%q err=%v", len(pageBody.Items), pageBody.NextCursor, err)
	}

	// Run reads while a representative normalization commits to exercise the
	// same concurrent PostgreSQL path used by collectors and executive views.
	snapshot := discovery.NewSnapshot(org, source, discovery.SourceEndpoint, discovery.Collector{ID: "scale", Name: "Scale", Version: "test", Mode: "managed"})
	snapshot.Sequence = 1
	var wait sync.WaitGroup
	errors := make(chan error, len(paths)+1)
	for _, path := range paths {
		wait.Add(1)
		go func(path string) {
			defer wait.Done()
			if response := query(path); response.Code != http.StatusOK {
				errors <- fmt.Errorf("concurrent %s returned %d", path, response.Code)
			}
		}(path)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		errors <- applyTestSnapshot(ctx, pool, snapshot)
	}()
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}
