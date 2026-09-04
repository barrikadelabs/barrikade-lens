package hub

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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
	_, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) SELECT $1,'scale-'||value,'runtime','runtime:'||value,'Runtime '||value,'{"installed":true}'::jsonb,'likely','{}',true,false,now(),now() FROM generate_series(1,1000000) value`, org)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,surface,system_role,system_type,product_id,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest) SELECT organization_id,id,'endpoint','system','agent_tool','runtime-'||id,'agent_tool','installed','none',false,confidence,true,first_seen_at,last_seen_at,id FROM entities WHERE organization_id=$1`, org)
	if err != nil {
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
	for _, path := range []string{"/v1/overview?window=7d", "/v1/systems?limit=100", "/v1/entities?limit=100", "/v1/exposures?limit=100"} {
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
}
