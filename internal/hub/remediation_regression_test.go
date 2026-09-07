package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/cloud"
	"github.com/barrikadelabs/barrikade-lens/internal/identity"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
)

func remediationServer(t *testing.T) (*Server, string) {
	t.Helper()
	ctx, pool := integrationPool(t)
	org := "remediation-" + uuid.NewString()
	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("remediation-012345678901234567890123456789"), DevAdminToken: "remediation-admin", DefaultOrganizationID: org, PublicURL: "https://lens.test", ExposureEnabled: true, SelfServeEnabled: true, AWSConnectorEnabled: true, EndpointConnectorEnabled: true, EndpointHandoffEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	return server, org
}

func remediationCall(server *Server, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer remediation-admin")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func remediationSetup(t *testing.T, server *Server, kind string) (string, string) {
	t.Helper()
	external := ""
	if kind == "aws_account" {
		external = "123456789012"
	}
	body, _ := json.Marshal(map[string]any{"kind": kind, "display_name": "Regression fixture", "external_id": external})
	response := remediationCall(server, http.MethodPost, "/v1/environments/setup-sessions", string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("setup returned %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		EnvironmentID string `json:"environment_id"`
		Setup         struct {
			Code string `json:"enrollment_code"`
		} `json:"setup"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.EnvironmentID, result.Setup.Code
}

func TestRemediationDisconnectRevokesPendingEnrollment(t *testing.T) {
	server, _ := remediationServer(t)
	id, code := remediationSetup(t, server, "endpoint")
	if response := remediationCall(server, http.MethodDelete, "/v1/environments/"+id, ""); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	state, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "https://lens.test")
	if err != nil {
		t.Fatal(err)
	}
	response, _ := exchangeTestIdentity(t, server, state, code, "revoked-device")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked enrollment returned %d: %s", response.Code, response.Body.String())
	}
}

func TestRemediationSafeProviderFailureCommitsAuthError(t *testing.T) {
	server, org := remediationServer(t)
	id, _ := remediationSetup(t, server, "aws_account")
	response := remediationCall(server, http.MethodPost, "/v1/environments/"+id+"/verify", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("provider failure returned %d", response.Code)
	}
	var status string
	var errorCode *string
	if err := server.config.Pool.QueryRow(t.Context(), `SELECT connection_status,last_error_code FROM environment_connections WHERE organization_id=$1 AND id=$2`, org, id).Scan(&status, &errorCode); err != nil {
		t.Fatal(err)
	}
	if status != "auth_error" || errorCode == nil {
		t.Fatalf("safe failure state was not committed: status=%s code=%v", status, errorCode)
	}
}

type remediationAdapter struct{}

func (remediationAdapter) Verify(context.Context, cloud.Environment) (cloud.Verification, error) {
	return cloud.Verification{Principal: "test"}, nil
}
func (remediationAdapter) AcquireTemporaryCredentials(context.Context, cloud.Environment) (cloud.Credentials, error) {
	return cloud.Credentials{}, nil
}
func (remediationAdapter) Scan(context.Context, cloud.Environment, uint64, cloud.ProgressFunc) (discovery.Snapshot, error) {
	return discovery.Snapshot{}, nil
}

func TestRemediationExpiredSetupCannotVerify(t *testing.T) {
	server, org := remediationServer(t)
	server.config.CloudAdapters = cloud.Registry{"aws": remediationAdapter{}}
	id, _ := remediationSetup(t, server, "aws_account")
	if _, err := server.config.Pool.Exec(t.Context(), `UPDATE connector_setup_sessions SET expires_at=now()-interval '1 hour' WHERE organization_id=$1 AND environment_id=$2`, org, id); err != nil {
		t.Fatal(err)
	}
	response := remediationCall(server, http.MethodPost, "/v1/environments/"+id+"/verify", "")
	if response.Code != http.StatusGone {
		t.Fatalf("expired setup returned %d: %s", response.Code, response.Body.String())
	}
}

func TestRemediationFailedCommitNeverReturnsSuccess(t *testing.T) {
	server, org := remediationServer(t)
	request := httptest.NewRequest(http.MethodPost, "/regression", nil)
	request = request.WithContext(context.WithValue(request.Context(), principalKey{}, Principal{OrganizationID: org, Subject: "regression"}))
	response := httptest.NewRecorder()
	server.tenantTransaction(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := server.db(r.Context()).Exec(r.Context(), `CREATE TEMP TABLE remediation_parent(id int PRIMARY KEY); CREATE TEMP TABLE remediation_child(id int REFERENCES remediation_parent(id) DEFERRABLE INITIALLY DEFERRED); INSERT INTO remediation_child VALUES(99)`); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "saved"})
	})).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "saved") {
		t.Fatalf("commit failure leaked success: %d %s", response.Code, response.Body.String())
	}
}

func TestRemediationClerkMembershipUpsertsOrganization(t *testing.T) {
	server, _ := remediationServer(t)
	tx, err := server.config.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	org := "clerk-order-" + uuid.NewString()
	body, _ := json.Marshal(map[string]any{"role": "org:admin", "organization": map[string]string{"id": org, "name": "Webhook first"}, "public_user_data": map[string]string{"user_id": "user_1"}})
	if err = applyClerkLifecycleEventAt(t.Context(), tx, "organizationMembership.created", body, time.Now()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRow(t.Context(), `SELECT count(*) FROM organizations WHERE id=$1`, org).Scan(&count); err != nil || count != 1 {
		t.Fatalf("organization was not upserted: count=%d err=%v", count, err)
	}
}

func TestRemediationDisconnectedCollectorJWTIsRejected(t *testing.T) {
	server, _ := remediationServer(t)
	id, code := remediationSetup(t, server, "endpoint")
	state, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "https://lens.test")
	if err != nil {
		t.Fatal(err)
	}
	response, _ := exchangeTestIdentity(t, server, state, code, "jwt-device")
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	if disconnected := remediationCall(server, http.MethodDelete, "/v1/environments/"+id, ""); disconnected.Code != http.StatusOK {
		t.Fatal(disconnected.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/session", nil)
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	result := httptest.NewRecorder()
	server.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusUnauthorized {
		t.Fatalf("revoked collector JWT returned %d", result.Code)
	}
}

func TestRemediationSnapshotBackpressureAllowsOneActiveJobPerSource(t *testing.T) {
	server, org := remediationServer(t)
	source := "busy-source-" + uuid.NewString()
	if err := insertTestSource(t.Context(), server.config.Pool, org, source, "endpoint", "Busy endpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.config.Pool.Exec(t.Context(), `INSERT INTO ingestion_jobs(id,organization_id,source_id,snapshot_id,status,payload) VALUES($1,$2,$3,$4,'processing','{}')`, uuid.New(), org, source, uuid.New()); err != nil {
		t.Fatal(err)
	}
	snapshot := discovery.NewSnapshot(org, source, discovery.SourceEndpoint, discovery.Collector{ID: "queue-test", Name: "Queue fixture", Version: "test", Mode: "managed"})
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	response := remediationCall(server, http.MethodPost, "/v1/discovery/snapshots", string(payload))
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "15" {
		t.Fatalf("concurrent source submission returned %d retry=%q: %s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
}

func TestRemediationDuplicateSequenceDoesNotFailConnection(t *testing.T) {
	server, org := remediationServer(t)
	source := "duplicate-source-" + uuid.NewString()
	if err := insertTestSource(t.Context(), server.config.Pool, org, source, "endpoint", "Duplicate endpoint"); err != nil {
		t.Fatal(err)
	}
	environmentID := uuid.New()
	if _, err := server.config.Pool.Exec(t.Context(), `INSERT INTO environment_connections(id,organization_id,kind,display_name,connection_status,target_id,source_id,last_result_status,last_result_at,created_by) VALUES($1,$2,'endpoint','Duplicate endpoint','connected',$3,$3,'complete',now(),'regression-test')`, environmentID, org, source); err != nil {
		t.Fatal(err)
	}
	if _, err := server.config.Pool.Exec(t.Context(), `UPDATE sources SET last_sequence=3 WHERE organization_id=$1 AND id=$2`, org, source); err != nil {
		t.Fatal(err)
	}
	snapshot := discovery.NewSnapshot(org, source, discovery.SourceEndpoint, discovery.Collector{ID: "duplicate-test", Name: "Duplicate fixture", Version: "test", Mode: "managed"})
	snapshot.Sequence = 3
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	jobID := uuid.New()
	if _, err = server.config.Pool.Exec(t.Context(), `INSERT INTO ingestion_jobs(id,organization_id,source_id,snapshot_id,status,payload) VALUES($1,$2,$3,$4,'pending',$5)`, jobID, org, source, snapshot.SnapshotID, payload); err != nil {
		t.Fatal(err)
	}
	processed, err := (Worker{Pool: server.config.Pool}).processOne(t.Context())
	if err != nil || !processed {
		t.Fatalf("duplicate processing returned processed=%v err=%v", processed, err)
	}
	var jobStatus, resultStatus string
	var errorCode *string
	if err = server.config.Pool.QueryRow(t.Context(), `SELECT status,error_code FROM ingestion_jobs WHERE id=$1`, jobID).Scan(&jobStatus, &errorCode); err != nil {
		t.Fatal(err)
	}
	if err = server.config.Pool.QueryRow(t.Context(), `SELECT last_result_status FROM environment_connections WHERE organization_id=$1 AND id=$2`, org, environmentID).Scan(&resultStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "complete" || errorCode != nil || resultStatus != "complete" {
		t.Fatalf("duplicate changed health: job=%s error=%v result=%s", jobStatus, errorCode, resultStatus)
	}
}

func TestRemediationDistributedRateLimitRejectsExcessRequests(t *testing.T) {
	server, _ := remediationServer(t)
	handler := server.rateLimit("regression_"+uuid.NewString(), 1, time.Minute, remoteRequestKey, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	call := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/limited", nil)
		request.RemoteAddr = "192.0.2.10:4242"
		response := httptest.NewRecorder()
		handler(response, request)
		return response
	}
	if first := call(); first.Code != http.StatusOK {
		t.Fatalf("first request returned %d", first.Code)
	}
	if second := call(); second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" {
		t.Fatalf("excess request returned %d retry=%q", second.Code, second.Header().Get("Retry-After"))
	}
}

func TestRemediationAccountDeletionUsesLensGovernedClerkCall(t *testing.T) {
	server, org := remediationServer(t)
	called := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/users/user_delete" || r.Header.Get("Authorization") != "Bearer clerk-secret" {
			t.Errorf("unexpected Clerk deletion request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer provider.Close()
	server.config.ClerkAPIBaseURL = provider.URL
	server.config.ClerkSecretKey = "clerk-secret"
	if _, err := server.config.Pool.Exec(t.Context(), `INSERT INTO workspace_memberships(organization_id,user_id,role,status) VALUES($1,'clerk:user_delete','admin','active')`, org); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/v1/account", nil)
	request = request.WithContext(context.WithValue(request.Context(), principalKey{}, Principal{OrganizationID: org, Subject: "clerk:user_delete", Role: "admin"}))
	response := httptest.NewRecorder()
	server.tenantTransaction(http.HandlerFunc(server.deleteAccount)).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || called != 1 {
		t.Fatalf("account deletion returned %d provider_calls=%d: %s", response.Code, called, response.Body.String())
	}
	var membershipStatus, userStatus string
	if err := server.config.Pool.QueryRow(t.Context(), `SELECT m.status,u.status FROM workspace_memberships m JOIN managed_users u ON u.user_id=m.user_id WHERE m.organization_id=$1 AND m.user_id='clerk:user_delete'`, org).Scan(&membershipStatus, &userStatus); err != nil {
		t.Fatal(err)
	}
	if membershipStatus != "deleted" || userStatus != "deleted" {
		t.Fatalf("deleted identity remained active: membership=%s user=%s", membershipStatus, userStatus)
	}
}

func TestRemediationHandoffRotationAndDisconnectAreTerminal(t *testing.T) {
	server, _ := remediationServer(t)
	id, _ := remediationSetup(t, server, "endpoint")
	created := remediationCall(server, http.MethodPost, "/v1/environments/"+id+"/handoffs", "{}")
	if created.Code != http.StatusCreated {
		t.Fatal(created.Code, created.Body.String())
	}
	var handoff struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(strings.SplitN(handoff.URL, "#", 2)[1], "token=")
	resolve := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/public/endpoint-handoffs/resolve", strings.NewReader(`{"token":"`+token+`","platform":"linux"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if first := resolve(); first.Code != http.StatusOK {
		t.Fatalf("first handoff resolve returned %d: %s", first.Code, first.Body.String())
	}
	if second := resolve(); second.Code != http.StatusOK {
		t.Fatalf("handoff should rotate a still-unused command, got %d", second.Code)
	}
	if disconnected := remediationCall(server, http.MethodDelete, "/v1/environments/"+id, ""); disconnected.Code != http.StatusOK {
		t.Fatal(disconnected.Code)
	}
	if terminal := resolve(); terminal.Code != http.StatusGone {
		t.Fatalf("disconnected handoff returned %d", terminal.Code)
	}
}

func TestRemediationExecutiveOwnershipAndInventoryAgree(t *testing.T) {
	server, org := remediationServer(t)
	source := "owner-source-" + uuid.NewString()
	if err := insertTestSource(t.Context(), server.config.Pool, org, source, "endpoint", "Ownership endpoint"); err != nil {
		t.Fatal(err)
	}
	snapshot := discovery.NewSnapshot(org, source, discovery.SourceEndpoint, discovery.Collector{ID: "ownership-test", Name: "Ownership fixture", Version: "test", Mode: "managed"})
	build := builder.New(snapshot)
	evidence := build.AddEvidence(builder.Observation{DetectorID: "ownership.fixture", DetectorVersion: "1", Method: "fixture", Family: "process", Specificity: "high", Locator: "fixture", Authoritative: true})
	evidenceOwned := build.AddEntity(discovery.KindAgent, "owned:evidence", "Evidence owned", map[string]any{"running_at_scan": true, "product_id": "owned-evidence", "product_category": "autonomous_agent", "source_surface": "endpoint"}, evidence)
	operatorOwned := build.AddEntity(discovery.KindAgent, "owned:operator", "Operator owned", map[string]any{"running_at_scan": true, "product_id": "owned-operator", "product_category": "autonomous_agent", "source_surface": "endpoint"}, evidence)
	owner := build.AddEntity(discovery.KindUser, "owner:user", "Security team", map[string]any{}, evidence)
	build.AddRelationship(discovery.RelationshipOwnedBy, evidenceOwned, owner, map[string]any{"authoritative": true}, evidence)
	result, err := build.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if err = applyTestSnapshot(t.Context(), server.config.Pool, result); err != nil {
		t.Fatal(err)
	}
	if _, err = server.config.Pool.Exec(t.Context(), `INSERT INTO entity_context(organization_id,entity_id,owner_name,owner_type,updated_by) VALUES($1,$2,'Platform Security','team','test')`, org, operatorOwned); err != nil {
		t.Fatal(err)
	}
	findingID := "ownership-finding-" + uuid.NewString()
	if _, err = server.config.Pool.Exec(t.Context(), `INSERT INTO exposure_findings(organization_id,id,root_entity_id,rule_id,rule_version,severity,title,explanation,recommended_next_step,path,evidence_bases) VALUES($1,$2,$3,'ownership.fixture','1','high','Review evidence-owned system','Ownership fixture','Confirm the accountable team','[]',ARRAY['observed'])`, org, findingID, evidenceOwned); err != nil {
		t.Fatal(err)
	}
	overview := remediationCall(server, http.MethodGet, "/v1/overview?window=7d", "")
	if overview.Code != http.StatusOK {
		t.Fatal(overview.Code, overview.Body.String())
	}
	var summary struct {
		Executive struct {
			Systems struct {
				Known int `json:"known"`
			} `json:"systems"`
			Ownership struct {
				Owned   int `json:"owned"`
				Unowned int `json:"unowned"`
			} `json:"effective_ownership"`
			Findings struct {
				Fresh int `json:"fresh"`
			} `json:"findings"`
		} `json:"executive_summary"`
	}
	if err = json.Unmarshal(overview.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	systems := remediationCall(server, http.MethodGet, "/v1/systems?freshness=all", "")
	var listed struct {
		Items []struct {
			Effective struct {
				Owned bool `json:"owned"`
			} `json:"effective_ownership"`
		} `json:"items"`
	}
	if err = json.Unmarshal(systems.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if summary.Executive.Systems.Known != len(listed.Items) || summary.Executive.Ownership.Owned != 2 || summary.Executive.Ownership.Unowned != 0 {
		t.Fatalf("cross-view ownership mismatch: summary=%+v list=%+v", summary, listed)
	}
	for _, item := range listed.Items {
		if !item.Effective.Owned {
			t.Fatal("system list disagrees with effective ownership summary")
		}
	}
	findings := remediationCall(server, http.MethodGet, "/v1/exposures?severity=high&freshness=fresh&owner_status=owned", "")
	var findingList struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err = json.Unmarshal(findings.Body.Bytes(), &findingList); err != nil {
		t.Fatalf("decode findings: %v body=%s", err, findings.Body.String())
	}
	detail := remediationCall(server, http.MethodGet, "/v1/exposures/"+findingID, "")
	system := remediationCall(server, http.MethodGet, "/v1/systems/"+evidenceOwned, "")
	var systemBody struct {
		Exposure struct {
			Total int `json:"total"`
		} `json:"exposure_summary"`
	}
	if err = json.Unmarshal(system.Body.Bytes(), &systemBody); err != nil {
		t.Fatal(err)
	}
	if findings.Code != http.StatusOK || detail.Code != http.StatusOK || system.Code != http.StatusOK || summary.Executive.Findings.Fresh != 1 || len(findingList.Items) != 1 || findingList.Items[0].ID != findingID || systemBody.Exposure.Total != 1 {
		t.Fatalf("overview/list/detail finding mismatch: overview=%d list=%+v detail=%d system=%+v", summary.Executive.Findings.Fresh, findingList.Items, detail.Code, systemBody)
	}
}
