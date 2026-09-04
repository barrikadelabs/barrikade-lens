package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestExposureVerticalSliceAndContextRBAC(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "exposure-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'Exposure test')`, org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	root := discovery.StableID(org, discovery.KindAgent, "root")
	connector := discovery.StableID(org, discovery.KindMCPServer, "connector")
	service := discovery.StableID(org, discovery.KindAPIService, "catalog:fixture")
	if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES
		($1,$2,'agent','Fixture agent','{"running_at_scan":true}','confirmed',ARRAY['fixture'],true,false,now(),now()),
		($1,$3,'mcp_server','Fixture connector','{"configured":true,"enabled":true,"host":"api.example.test","credential_present":true}','confirmed',ARRAY['fixture'],true,false,now(),now()),
		($1,$4,'api_service','Fixture API','{"catalog_enriched":true,"api_id":"fixture-api","version":"1"}','confirmed',ARRAY['catalog:fixture'],true,false,now(),now())`, org, root, connector, service); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,surface,system_role,system_type,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest) VALUES($1,$2,'endpoint','system','autonomous_agent','autonomous_agent','running','none',false,'confirmed',true,now(),now(),'fixture')`, org, root); err != nil {
		t.Fatal(err)
	}
	rootEdge, catalogEdge := discovery.RelationshipID(org, discovery.RelationshipConnectsTo, root, connector), discovery.RelationshipID(org, discovery.RelationshipConnectsTo, connector, service)
	if _, err := pool.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at) VALUES
		($1,$2,'connects_to',$3,$4,'{}','confirmed',true,false,now(),now()),
		($1,$5,'connects_to',$4,$6,'{"catalog_enriched":true,"catalog_provider":"fixture"}','confirmed',true,false,now(),now())`, org, rootEdge, root, connector, catalogEdge, service); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO catalog_sources(organization_id,id,provider_type,display_name,configuration) VALUES($1,'fixture','oak','Fixture','{}')`, []any{org}},
		{`INSERT INTO catalog_matches(organization_id,entity_id,source_id,api_id,confidence,status,metadata) VALUES($1,$2,'fixture','fixture-api','confirmed','linked','{"reason":"administrator-reviewed catalogue link"}')`, []any{org, connector}},
		{`INSERT INTO catalog_api_operations(organization_id,source_id,api_id,operation_key,operation_id,method,path,capability_class) VALUES($1,'fixture','fixture-api','delete','deleteRecord','DELETE','/records/{id}','destructive_potential')`, []any{org}},
		{`INSERT INTO entity_context(organization_id,entity_id,sensitivity,data_categories,updated_by) VALUES($1,$2,'restricted',ARRAY['health'],'test-admin')`, []any{org, root}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := recomputeOrganizationExposures(ctx, tx, org); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT rule_id,severity FROM exposure_findings WHERE organization_id=$1 AND current=true`, org)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var rule, severity string
		if err := rows.Scan(&rule, &severity); err != nil {
			t.Fatal(err)
		}
		got[rule] = severity
	}
	rows.Close()
	for rule, severity := range map[string]string{"sensitive_public_destination": "high", "credentialed_external_connector": "high", "state_changing_api_potential": "high", "missing_owner": "medium"} {
		if got[rule] != severity {
			t.Errorf("%s severity=%q, want %q; all=%v", rule, got[rule], severity, got)
		}
	}

	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "exposure-admin", DefaultOrganizationID: org, ExposureEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	disabledServer, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "exposure-admin", DefaultOrganizationID: org})
	if err != nil {
		t.Fatal(err)
	}
	disabledRequest := httptest.NewRequest(http.MethodGet, "/v1/exposures", nil)
	disabledRequest.Header.Set("Authorization", "Bearer exposure-admin")
	disabledResponse := httptest.NewRecorder()
	disabledServer.Handler().ServeHTTP(disabledResponse, disabledRequest)
	if disabledResponse.Code != http.StatusNotFound {
		t.Fatalf("disabled exposure route returned %d", disabledResponse.Code)
	}
	collector, _, err := server.auth.issueAccessToken(org, "collector", []string{"discovery:write"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/v1/entities/"+root+"/context", strings.NewReader(`{"owner_name":"should fail","owner_type":"team","data_categories":[]}`))
	request.Header.Set("Authorization", "Bearer "+collector)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("collector context write returned %d: %s", response.Code, response.Body.String())
	}
	registryToken := "registry-read-only"
	if _, err := pool.Exec(ctx, `INSERT INTO service_accounts(id,organization_id,name,token_hash,scopes) VALUES($1,$2,'registry',$3,ARRAY['inventory:read'])`, uuid.New(), org, tokenHash(registryToken)); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPut, "/v1/entities/"+root+"/context", strings.NewReader(`{"owner_name":"should also fail","owner_type":"team","data_categories":[]}`))
	request.Header.Set("Authorization", "Bearer "+registryToken)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("Registry service context write returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPut, "/v1/entities/"+root+"/context", strings.NewReader(`{"owner_name":"Security Engineering","owner_type":"team","sensitivity":"restricted","data_categories":["health"]}`))
	request.Header.Set("Authorization", "Bearer exposure-admin")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("administrator context write returned %d: %s", response.Code, response.Body.String())
	}
	var history, missingOwnerCurrent int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entity_context_history WHERE organization_id=$1 AND entity_id=$2`, org, root).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exposure_findings WHERE organization_id=$1 AND root_entity_id=$2 AND rule_id='missing_owner' AND current=true`, org, root).Scan(&missingOwnerCurrent); err != nil {
		t.Fatal(err)
	}
	if history != 1 || missingOwnerCurrent != 0 {
		t.Fatalf("context audit/resolution mismatch: history=%d missing_owner_current=%d", history, missingOwnerCurrent)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/systems/"+root+"/exposure-map", nil)
	request.Header.Set("Authorization", "Bearer exposure-admin")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "catalog_potential") || !strings.Contains(strings.ToLower(response.Body.String()), "effective credential scope") {
		t.Fatalf("unexpected exposure map response %d: %s", response.Code, response.Body.String())
	}
}
