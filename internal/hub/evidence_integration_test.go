package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSystemsDefaultToFreshIdentityAndEvidenceIsActionable(t *testing.T) {
	ctx, pool := integrationPool(t)
	orgID := "evidence-" + uuid.NewString()
	freshTarget := "target:fresh:" + uuid.NewString()
	staleTarget := "target:stale:" + uuid.NewString()
	freshEntity := "entity:fresh:" + uuid.NewString()
	staleEntity := "entity:stale:" + uuid.NewString()
	skillEntity := "entity:skill:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'evidence test')`, orgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, orgID) })
	for _, targetID := range []string{freshTarget, staleTarget} {
		if err := insertTestSource(ctx, pool, orgID, targetID, "endpoint", "same-host.local"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE discovery_targets SET last_seen_at=CASE id WHEN $2 THEN now() ELSE now()-interval '30 days' END,last_full_at=CASE id WHEN $2 THEN now() ELSE now()-interval '30 days' END WHERE organization_id=$1 AND id IN ($2,$3)`, orgID, freshTarget, staleTarget); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES
		($1,$2,'runtime','fresh:claude','Claude Code','{"product_id":"claude","product_category":"agent_tool","source_surface":"endpoint","configured":true,"configuration_scope":"user"}','confirmed','{}',true,false,now(),now()),
		($1,$3,'runtime','stale:claude','Claude Code','{"product_id":"claude","product_category":"agent_tool","source_surface":"endpoint","configured":true}','confirmed','{}',true,false,now()-interval '30 days',now()-interval '30 days'),
		($1,$4,'skill','skill:imagegen','imagegen','{"configured":true,"descriptor_valid":true,"descriptor_format":"agent_skills","descriptor_relative":"imagegen","skill_scope":"user","provider_product_id":"claude","declared_purpose":"Generate or edit raster images","allowed_tools":["Read","Write"],"descriptor_fields":["name","description","allowed-tools"],"description_present":true,"license_declared":false,"allowed_tools_declared":true,"source_surface":"endpoint"}','confirmed','{}',true,false,now(),now())`, orgID, freshEntity, staleEntity, skillEntity); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entity_posture(organization_id,entity_id,target_id,surface,system_role,system_type,product_id,product_category,discovery_state,network_scope,attributed,confidence,current,first_seen_at,last_seen_at,material_digest) VALUES
		($1,$2,$4,'endpoint','system','agent_tool','claude','agent_tool','configured','none',false,'confirmed',true,now(),now(),'fresh'),
		($1,$3,$5,'endpoint','system','agent_tool','claude','agent_tool','configured','none',false,'confirmed',true,now()-interval '30 days',now()-interval '30 days','stale'),
		($1,$6,$4,'endpoint','component',NULL,NULL,NULL,'configured','none',false,'confirmed',true,now(),now(),'skill')`, orgID, freshEntity, staleEntity, freshTarget, staleTarget, skillEntity); err != nil {
		t.Fatal(err)
	}
	pathReference := "sha256:opaque-path-reference"
	contentHash := "sha256:content-integrity-reference"
	if _, err := pool.Exec(ctx, `INSERT INTO evidence_observations(organization_id,snapshot_id,evidence_id,source_id,entity_ids,detector_id,detector_version,method,family,specificity,locator,content_hash,observed_at) VALUES
		($1,$2,'ev-1',$3,ARRAY[$4],'runtime.claude','2','config_shape','configuration','high',$5,$6,now()-interval '1 minute'),
		($1,$7,'ev-2',$3,ARRAY[$4],'runtime.claude','2','config_shape','configuration','high',$5,$6,now()),
		($1,$8,'ev-skill',$3,ARRAY[$4,$9],'claude.skills','2','skill_descriptor','skill','high','sha256:skill-path','sha256:skill-content',now())`, orgID, uuid.New(), freshTarget, freshEntity, pathReference, contentHash, uuid.New(), uuid.New(), skillEntity); err != nil {
		t.Fatal(err)
	}

	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "evidence-admin", DefaultOrganizationID: orgID})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer evidence-admin")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
		return response
	}
	assertSystemIDs(t, get("/v1/systems?sort=name").Body.Bytes(), []string{freshEntity})
	assertSystemIDs(t, get("/v1/systems?sort=name&freshness=stale").Body.Bytes(), []string{staleEntity})
	assertSystemIDs(t, get("/v1/systems?sort=name&freshness=all").Body.Bytes(), []string{freshEntity, staleEntity})
	assertSystemIDs(t, get("/v1/entities?sort=name&freshness=fresh").Body.Bytes(), []string{freshEntity, skillEntity})
	assertSystemIDs(t, get("/v1/entities?sort=name&freshness=stale").Body.Bytes(), []string{staleEntity})
	assertSystemIDs(t, get("/v1/entities?sort=name&freshness=all").Body.Bytes(), []string{freshEntity, staleEntity, skillEntity})

	detail := map[string]any{}
	if err := json.Unmarshal(get("/v1/systems/"+freshEntity).Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	evidence, _ := detail["evidence"].([]any)
	if len(evidence) != 2 {
		t.Fatalf("expected one collapsed configuration finding and one exact skill finding, got %d: %v", len(evidence), evidence)
	}
	var finding, skillFinding map[string]any
	for _, value := range evidence {
		candidate, _ := value.(map[string]any)
		if candidate["method"] == "config_shape" {
			finding = candidate
		}
		if candidate["method"] == "skill_descriptor" {
			skillFinding = candidate
		}
	}
	if finding == nil || skillFinding == nil {
		t.Fatalf("expected configuration and skill findings: %v", evidence)
	}
	for _, field := range []string{"title", "summary", "location", "why_it_matched", "investigation_hint", "matched_facts", "integrity"} {
		if finding[field] == nil || finding[field] == "" {
			t.Fatalf("deep evidence field %q is missing: %v", field, finding)
		}
	}
	if finding["location"] != "Protected endpoint location" {
		t.Fatalf("path hash should not be the primary finding location: %v", finding["location"])
	}
	integrity, _ := finding["integrity"].(map[string]any)
	if integrity["locator_reference"] != pathReference || integrity["content_hash"] != contentHash {
		t.Fatalf("integrity references were not retained separately: %v", integrity)
	}
	encoded, _ := json.Marshal(map[string]any{"title": finding["title"], "summary": finding["summary"], "location": finding["location"], "why": finding["why_it_matched"], "next": finding["investigation_hint"], "facts": finding["matched_facts"]})
	if strings.Contains(string(encoded), "opaque-path-reference") || strings.Contains(string(encoded), "content-integrity-reference") {
		t.Fatalf("opaque hashes escaped into the user-facing evidence summary: %s", encoded)
	}
	subject, _ := skillFinding["subject"].(map[string]any)
	if subject["entity_id"] != skillEntity || subject["entity_kind"] != "skill" || subject["name"] != "imagegen" {
		t.Fatalf("skill evidence did not resolve its exact linked resource: %v", skillFinding)
	}
	skillText, _ := json.Marshal(skillFinding)
	for _, expected := range []string{"Validated skill descriptor", "imagegen", "Claude Code", "~/.claude/skills/imagegen/SKILL.md", "Generate or edit raster images", "Allowed Tools", "Read, Write", "Descriptor Valid"} {
		if !strings.Contains(string(skillText), expected) {
			t.Fatalf("deep skill evidence omitted %q: %s", expected, skillText)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE entities SET current=false,stale=true WHERE organization_id=$1 AND id=$2`, orgID, skillEntity); err != nil {
		t.Fatal(err)
	}
	var refreshed map[string]any
	if err := json.Unmarshal(get("/v1/systems/"+freshEntity).Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	refreshedEvidence, _ := refreshed["evidence"].([]any)
	for _, value := range refreshedEvidence {
		candidate, _ := value.(map[string]any)
		if candidate["method"] == "skill_descriptor" {
			t.Fatalf("removed skill evidence remained on the current runtime view: %v", candidate)
		}
	}
}

func assertSystemIDs(t *testing.T, body []byte, expected []string) {
	t.Helper()
	var result struct {
		Items []struct {
			ID              string `json:"id"`
			TargetFreshness string `json:"target_freshness"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	actual := map[string]bool{}
	for _, item := range result.Items {
		actual[item.ID] = true
	}
	if len(actual) != len(expected) {
		t.Fatalf("expected systems %v, got %v", expected, actual)
	}
	for _, id := range expected {
		if !actual[id] {
			t.Fatalf("expected system %q in %v", id, actual)
		}
	}
}
