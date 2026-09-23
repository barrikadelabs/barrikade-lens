package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestTopologyPathsResolveEvidenceInBothDirections(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "topology-" + uuid.NewString()
	source := "source:" + uuid.NewString()
	ids := []string{"agent:" + uuid.NewString(), "mcp:" + uuid.NewString(), "tool:" + uuid.NewString(), "api:" + uuid.NewString()}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'topology test')`, org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	if err := insertTestSource(ctx, pool, org, source, "endpoint", "topology source"); err != nil {
		t.Fatal(err)
	}
	for index, kind := range []string{"agent", "mcp_server", "tool", "api_service"} {
		if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,$3,$4,'{}','confirmed','{}',true,false,now(),now())`, org, ids[index], kind, kind); err != nil {
			t.Fatal(err)
		}
	}
	relationIDs := []string{"edge:" + uuid.NewString(), "edge:" + uuid.NewString(), "edge:" + uuid.NewString()}
	for index, kind := range []string{"connects_to", "provides", "connects_to"} {
		if _, err := pool.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at,surfaces,observation_states) VALUES($1,$2,$3,$4,$5,'{}','confirmed',true,false,now(),now(),ARRAY['endpoint'],ARRAY['declared'])`, org, relationIDs[index], kind, ids[index], ids[index+1]); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO source_relationships(organization_id,source_id,relationship_id,last_seen_at,last_seen_sequence,current,stale) VALUES($1,$2,$3,now(),1,true,false)`, org, source, relationIDs[index]); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO evidence_observations(organization_id,snapshot_id,evidence_id,source_id,entity_ids,relationship_ids,detector_id,detector_version,method,family,specificity,observed_at) VALUES($1,$2,$3,$4,ARRAY[$5,$6],ARRAY[$7],'fixture','1','descriptor','mcp_configuration','high',now())`, org, uuid.New(), "evidence:"+uuid.NewString(), source, ids[index], ids[index+1], relationIDs[index]); err != nil {
			t.Fatal(err)
		}
	}
	// A current edge without a linked observation must not appear as a path.
	unproven := "edge:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'connects_to',$3,$4,'{}','possible',true,false,now(),now())`, org, unproven, ids[0], ids[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO source_relationships(organization_id,source_id,relationship_id,last_seen_at,last_seen_sequence,current,stale) VALUES($1,$2,$3,now(),1,true,false)`, org, source, unproven); err != nil {
		t.Fatal(err)
	}
	// More recent unsupported links must not exhaust the per-node fanout before
	// an older evidence-backed link can be explored.
	for index := 0; index < 15; index++ {
		unprovenTarget := "unproven:" + uuid.NewString()
		unprovenEdge := "edge:" + uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'api_service','Unsupported destination','{}','possible','{}',true,false,now(),now())`, org, unprovenTarget); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'connects_to',$3,$4,'{}','possible',true,false,now(),now()+interval '1 minute')`, org, unprovenEdge, ids[0], unprovenTarget); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO source_relationships(organization_id,source_id,relationship_id,last_seen_at,last_seen_sequence,current,stale) VALUES($1,$2,$3,now(),1,true,false)`, org, source, unprovenEdge); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "topology-admin", DefaultOrganizationID: org})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		start, direction string
		expectedEnd      string
	}{{ids[0], "downstream", ids[3]}, {ids[3], "upstream", ids[0]}} {
		request := httptest.NewRequest(http.MethodGet, "/v1/topology/paths?entity_id="+test.start+"&direction="+test.direction, nil)
		request.Header.Set("Authorization", "Bearer topology-admin")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("topology returned %d: %s", response.Code, response.Body.String())
		}
		var body struct {
			Paths []struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
				Edges []struct {
					Evidence map[string]any `json:"evidence"`
				} `json:"edges"`
			} `json:"paths"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, path := range body.Paths {
			for _, edge := range path.Edges {
				if edge.Evidence["evidence_id"] == nil {
					t.Fatalf("unproven edge appeared: %#v", path)
				}
			}
			if len(path.Nodes) != 4 || path.Nodes[3].ID != test.expectedEnd {
				continue
			}
			if len(path.Edges) != 3 {
				t.Fatalf("missing path edges: %#v", path)
			}
			for _, edge := range path.Edges {
				if edge.Evidence["evidence_id"] == nil || edge.Evidence["source_id"] != source {
					t.Fatalf("edge evidence missing: %#v", edge)
				}
			}
			found = true
		}
		if !found {
			t.Fatalf("%s path to %s missing: %s", test.direction, test.expectedEnd, response.Body.String())
		}
	}
	otherOrg := "topology-other-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'other topology tenant')`, otherOrg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, otherOrg) })
	foreignEntity := "foreign:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'agent','Other tenant','{}','confirmed','{}',true,false,now(),now())`, otherOrg, foreignEntity); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/topology/paths?entity_id="+foreignEntity+"&direction=downstream", nil)
	request.Header.Set("Authorization", "Bearer topology-admin")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign entity was visible: %d %s", response.Code, response.Body.String())
	}
}
