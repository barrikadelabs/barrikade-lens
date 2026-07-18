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
	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "scale-admin", DefaultOrganizationID: org})
	if err != nil {
		t.Fatal(err)
	}
	query := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/entities?limit=100", nil)
		request.Header.Set("Authorization", "Bearer scale-admin")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if response := query(); response.Code != 200 {
		t.Fatalf("warmup query returned %d", response.Code)
	}
	started := time.Now()
	response := query()
	if response.Code != 200 {
		t.Fatalf("query returned %d", response.Code)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("million-entity inventory query took %s", elapsed)
	}
}
