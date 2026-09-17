package hub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/identity"
	"github.com/google/uuid"
)

type policyCredentialFixture struct {
	PolicyID     string
	CredentialID string
	Code         string
}

func policyAPICall(server *Server, token, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func createPolicyCredentialFixture(t *testing.T, server *Server, token string, maxEnrollments int) policyCredentialFixture {
	t.Helper()
	policyResponse := policyAPICall(server, token, http.MethodPost, "/v1/deployment-policies", `{"name":"Engineering laptops","description":"Managed engineering fleet","source_type":"endpoint","discovery_depth":"deep","configuration":{"include_local_models":true},"expected_device_count":25,"credential_expires_in_seconds":3600,"max_enrollments":25}`)
	if policyResponse.Code != http.StatusCreated {
		t.Fatalf("create policy returned %d: %s", policyResponse.Code, policyResponse.Body.String())
	}
	var policy struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(policyResponse.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	credentialResponse := policyAPICall(server, token, http.MethodPost, "/v1/deployment-policies/"+policy.ID+"/enrollment-credentials", `{"expires_in_seconds":3600,"max_enrollments":`+jsonNumber(maxEnrollments)+`}`)
	if credentialResponse.Code != http.StatusCreated {
		t.Fatalf("issue credential returned %d: %s", credentialResponse.Code, credentialResponse.Body.String())
	}
	var credential struct {
		Code       string `json:"code"`
		Credential struct {
			ID string `json:"id"`
		} `json:"credential"`
	}
	if err := json.Unmarshal(credentialResponse.Body.Bytes(), &credential); err != nil {
		t.Fatal(err)
	}
	if policy.ID == "" || credential.Credential.ID == "" || credential.Code == "" {
		t.Fatalf("policy credential response omitted identifiers or display-once code: %s", credentialResponse.Body.String())
	}
	return policyCredentialFixture{PolicyID: policy.ID, CredentialID: credential.Credential.ID, Code: credential.Code}
}

func jsonNumber(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func exchangePolicyIdentity(t *testing.T, server *Server, state identity.State, code, hostname, sourceType string) (*httptest.ResponseRecorder, enrollmentResult) {
	t.Helper()
	proof, err := state.Sign(code, hostname, "darwin", "arm64", "2.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"code": code, "hostname": hostname, "platform": "darwin", "architecture": "arm64", "collector_version": "2.0.0-test", "identity_public_key": state.PublicKey, "identity_proof": proof, "source_type": sourceType})
	request := httptest.NewRequest(http.MethodPost, "/v1/enrollment/exchange", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var result enrollmentResult
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return response, result
}

func TestDeploymentPolicyLifecycleAndTenantIsolation(t *testing.T) {
	server, orgID := newIdentityTestServer(t)
	fixture := createPolicyCredentialFixture(t, server, "identity-admin", 3)

	inspect := policyAPICall(server, "identity-admin", http.MethodGet, "/v1/deployment-policies/"+fixture.PolicyID, "")
	if inspect.Code != http.StatusOK || strings.Contains(inspect.Body.String(), fixture.Code) || strings.Contains(inspect.Body.String(), "code_hash") {
		t.Fatalf("policy inspection leaked credential material or failed: %d %s", inspect.Code, inspect.Body.String())
	}
	var detail struct {
		Status         string `json:"status"`
		RemainingCount int    `json:"remaining_count"`
		Credentials    []struct {
			Status         string `json:"status"`
			RemainingCount int    `json:"remaining_count"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(inspect.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Status != "active" || detail.RemainingCount != 3 || len(detail.Credentials) != 1 || detail.Credentials[0].RemainingCount != 3 {
		t.Fatalf("unexpected safe policy counts: %+v", detail)
	}
	updated := policyAPICall(server, "identity-admin", http.MethodPatch, "/v1/deployment-policies/"+fixture.PolicyID, `{"name":"Engineering fleet","discovery_depth":"standard","expected_device_count":null}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"name":"Engineering fleet"`) || !strings.Contains(updated.Body.String(), `"expected_device_count":null`) {
		t.Fatalf("policy update returned %d: %s", updated.Code, updated.Body.String())
	}
	rotated := policyAPICall(server, "identity-admin", http.MethodPost, "/v1/deployment-policies/"+fixture.PolicyID+"/enrollment-credentials", `{"expires_in_seconds":3600,"max_enrollments":2}`)
	if rotated.Code != http.StatusCreated || strings.Contains(rotated.Body.String(), fixture.Code) {
		t.Fatalf("credential rotation returned %d or repeated old secret: %s", rotated.Code, rotated.Body.String())
	}
	list := policyAPICall(server, "identity-admin", http.MethodGet, "/v1/deployment-policies", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), fixture.PolicyID) || strings.Contains(list.Body.String(), fixture.Code) {
		t.Fatalf("policy listing failed or leaked credential material: %d %s", list.Code, list.Body.String())
	}

	otherOrg := "policy-other-" + uuid.NewString()
	otherServer, err := NewServer(t.Context(), Config{Pool: server.config.Pool, WorkerPool: server.config.WorkerPool, JWTSecret: []byte("other-policy-012345678901234567890123456789"), DevAdminToken: "other-admin", DefaultOrganizationID: otherOrg, PublicURL: "http://lens.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = server.config.Pool.Exec(t.Context(), `DELETE FROM organizations WHERE id=$1`, otherOrg) })
	isolated := policyAPICall(otherServer, "other-admin", http.MethodGet, "/v1/deployment-policies/"+fixture.PolicyID, "")
	if isolated.Code != http.StatusNotFound {
		t.Fatalf("cross-organization policy lookup returned %d: %s", isolated.Code, isolated.Body.String())
	}
	var ownPolicyCount int
	if err := server.config.Pool.QueryRow(t.Context(), `SELECT count(*) FROM deployment_policies WHERE organization_id=$1`, orgID).Scan(&ownPolicyCount); err != nil || ownPolicyCount != 1 {
		t.Fatalf("unexpected policy persistence count=%d err=%v", ownPolicyCount, err)
	}
	var lifecycleAudits int
	if err := server.config.Pool.QueryRow(t.Context(), `SELECT count(DISTINCT event_type) FROM workspace_audit_events WHERE organization_id=$1 AND (target_id=$2::text OR target_id IN (SELECT id::text FROM enrollment_codes WHERE organization_id=$1 AND policy_id=$2::uuid)) AND event_type IN ('deployment_policy.created','deployment_policy.updated','deployment_policy.credential_issued','deployment_policy.credential_revoked')`, orgID, fixture.PolicyID).Scan(&lifecycleAudits); err != nil || lifecycleAudits != 4 {
		t.Fatalf("policy lifecycle audits missing: count=%d err=%v", lifecycleAudits, err)
	}
}

func TestDeploymentPolicyEnrollmentLineageAndReenrollment(t *testing.T) {
	server, orgID := newIdentityTestServer(t)
	fixture := createPolicyCredentialFixture(t, server, "identity-admin", 2)
	state, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")
	if err != nil {
		t.Fatal(err)
	}

	firstResponse, first := exchangePolicyIdentity(t, server, state, fixture.Code, "engineering.local", "endpoint")
	secondResponse, second := exchangePolicyIdentity(t, server, state, fixture.Code, "renamed.local", "endpoint")
	if firstResponse.Code != http.StatusOK || secondResponse.Code != http.StatusOK {
		t.Fatalf("policy enrollment failed: first=%d %s second=%d %s", firstResponse.Code, firstResponse.Body.String(), secondResponse.Code, secondResponse.Body.String())
	}
	if first.SourceID != second.SourceID || first.TargetID != second.TargetID || first.RefreshToken == second.RefreshToken {
		t.Fatalf("re-enrollment did not preserve identity and rotate credentials: first=%+v second=%+v", first, second)
	}

	var events int
	var sourcePolicy, sourceEnrollment, targetPolicy, targetEnrollment *uuid.UUID
	err = server.config.Pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM deployment_policy_enrollments WHERE organization_id=$1 AND policy_id=$2),
		(SELECT deployment_policy_id FROM sources WHERE organization_id=$1 AND id=$3),
		(SELECT deployment_enrollment_id FROM sources WHERE organization_id=$1 AND id=$3),
		(SELECT deployment_policy_id FROM discovery_targets WHERE organization_id=$1 AND id=$4),
		(SELECT deployment_enrollment_id FROM discovery_targets WHERE organization_id=$1 AND id=$4)`, orgID, fixture.PolicyID, first.SourceID, first.TargetID).Scan(&events, &sourcePolicy, &sourceEnrollment, &targetPolicy, &targetEnrollment)
	if err != nil {
		t.Fatal(err)
	}
	if events != 2 || sourcePolicy == nil || sourceEnrollment == nil || targetPolicy == nil || targetEnrollment == nil || sourcePolicy.String() != fixture.PolicyID || targetPolicy.String() != fixture.PolicyID || *sourceEnrollment != *targetEnrollment {
		t.Fatalf("policy lineage missing or inconsistent: events=%d source=%v/%v target=%v/%v", events, sourcePolicy, sourceEnrollment, targetPolicy, targetEnrollment)
	}

	inspect := policyAPICall(server, "identity-admin", http.MethodGet, "/v1/deployment-policies/"+fixture.PolicyID, "")
	var detail struct {
		EnrolledCount  int `json:"enrolled_count"`
		RemainingCount int `json:"remaining_count"`
		Credentials    []struct {
			Status        string `json:"status"`
			EnrolledCount int    `json:"enrolled_count"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(inspect.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.EnrolledCount != 1 || detail.RemainingCount != 0 || len(detail.Credentials) != 1 || detail.Credentials[0].Status != "exhausted" || detail.Credentials[0].EnrolledCount != 2 {
		t.Fatalf("unexpected enrollment counts after re-enrollment: %+v", detail)
	}
	if response := policyAPICall(server, "identity-admin", http.MethodDelete, "/v1/deployment-policies/"+fixture.PolicyID, ""); response.Code != http.StatusNoContent {
		t.Fatalf("policy revocation returned %d: %s", response.Code, response.Body.String())
	}
	var sourceActive bool
	var refreshTokens int
	if err := server.config.Pool.QueryRow(t.Context(), `SELECT revoked_at IS NULL,(SELECT count(*) FROM collector_refresh_tokens WHERE organization_id=$1 AND source_id=$2) FROM sources WHERE organization_id=$1 AND id=$2`, orgID, first.SourceID).Scan(&sourceActive, &refreshTokens); err != nil || !sourceActive || refreshTokens != 1 {
		t.Fatalf("policy revocation affected enrolled device: active=%v refresh_tokens=%d err=%v", sourceActive, refreshTokens, err)
	}
}

func TestDeploymentPolicyEnrollmentRejectionsAndRevocation(t *testing.T) {
	server, orgID := newIdentityTestServer(t)
	state, _ := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")

	mismatch := createPolicyCredentialFixture(t, server, "identity-admin", 1)
	response, _ := exchangePolicyIdentity(t, server, state, mismatch.Code, "mismatch.local", "repository")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("source mismatch returned %d: %s", response.Code, response.Body.String())
	}
	valid, _ := exchangePolicyIdentity(t, server, state, mismatch.Code, "mismatch.local", "endpoint")
	if valid.Code != http.StatusOK {
		t.Fatalf("source mismatch consumed the credential: %d %s", valid.Code, valid.Body.String())
	}

	expired := createPolicyCredentialFixture(t, server, "identity-admin", 1)
	if _, err := server.config.Pool.Exec(t.Context(), `UPDATE enrollment_codes SET expires_at=now()-interval '1 second' WHERE organization_id=$1 AND id=$2`, orgID, expired.CredentialID); err != nil {
		t.Fatal(err)
	}
	if response, _ := exchangePolicyIdentity(t, server, state, expired.Code, "expired.local", "endpoint"); response.Code != http.StatusUnauthorized {
		t.Fatalf("expired credential returned %d: %s", response.Code, response.Body.String())
	}

	revoked := createPolicyCredentialFixture(t, server, "identity-admin", 1)
	if response := policyAPICall(server, "identity-admin", http.MethodDelete, "/v1/deployment-policies/"+revoked.PolicyID+"/enrollment-credentials/"+revoked.CredentialID, ""); response.Code != http.StatusNoContent {
		t.Fatalf("credential revocation returned %d: %s", response.Code, response.Body.String())
	}
	if response, _ := exchangePolicyIdentity(t, server, state, revoked.Code, "revoked.local", "endpoint"); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked credential returned %d: %s", response.Code, response.Body.String())
	}

	policyRevoked := createPolicyCredentialFixture(t, server, "identity-admin", 1)
	if response := policyAPICall(server, "identity-admin", http.MethodDelete, "/v1/deployment-policies/"+policyRevoked.PolicyID, ""); response.Code != http.StatusNoContent {
		t.Fatalf("policy revocation returned %d: %s", response.Code, response.Body.String())
	}
	if response, _ := exchangePolicyIdentity(t, server, state, policyRevoked.Code, "policy-revoked.local", "endpoint"); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked policy credential returned %d: %s", response.Code, response.Body.String())
	}

	var rejectedAudits int
	if err := server.config.Pool.QueryRow(t.Context(), `SELECT count(*) FROM workspace_audit_events WHERE organization_id=$1 AND event_type='deployment_policy.enrollment_rejected'`, orgID).Scan(&rejectedAudits); err != nil || rejectedAudits < 3 {
		t.Fatalf("rejected enrollment audits missing: count=%d err=%v", rejectedAudits, err)
	}
}

func TestDeploymentPolicyConcurrentFinalUse(t *testing.T) {
	server, _ := newIdentityTestServer(t)
	fixture := createPolicyCredentialFixture(t, server, "identity-admin", 1)
	identities := make([]identity.State, 2)
	for index := range identities {
		identities[index], _ = identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")
	}
	statuses := make([]int, 2)
	var group sync.WaitGroup
	for index := range identities {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			response, _ := exchangePolicyIdentity(t, server, identities[index], fixture.Code, "concurrent-"+jsonNumber(index)+".local", "endpoint")
			statuses[index] = response.Code
		}(index)
	}
	group.Wait()
	if !((statuses[0] == http.StatusOK && statuses[1] == http.StatusUnauthorized) || (statuses[1] == http.StatusOK && statuses[0] == http.StatusUnauthorized)) {
		t.Fatalf("final credential use was not atomic: statuses=%v", statuses)
	}
}

func TestDeploymentPolicyConfigurationRejectsSecrets(t *testing.T) {
	server, _ := newIdentityTestServer(t)
	response := policyAPICall(server, "identity-admin", http.MethodPost, "/v1/deployment-policies", `{"name":"Unsafe","configuration":{"nested":{"api_token":"do-not-store"}}}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "secret_not_allowed") {
		t.Fatalf("secret-like configuration returned %d: %s", response.Code, response.Body.String())
	}

	if validatePolicyConfiguration(map[string]any{"rules": []any{[]any{map[string]any{"private_key": "hidden"}}}}) {
		t.Fatal("nested list secret-like configuration was accepted")
	}
}

func TestCredentialStatus(t *testing.T) {
	now := time.Now()
	revoked := now
	if credentialStatus(&revoked, now.Add(time.Hour), 1) != "revoked" || credentialStatus(nil, now.Add(-time.Second), 1) != "expired" || credentialStatus(nil, now.Add(time.Hour), 0) != "exhausted" || credentialStatus(nil, now.Add(time.Hour), 1) != "active" {
		t.Fatal("credential status precedence is incorrect")
	}
}
