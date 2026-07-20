package hub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/identity"
	"github.com/google/uuid"
)

type enrollmentResult struct {
	OrganizationID string `json:"organization_id"`
	SourceID       string `json:"source_id"`
	TargetID       string `json:"target_id"`
	Sequence       uint64 `json:"sequence"`
	RefreshToken   string `json:"refresh_token"`
}

func createTestEnrollmentCode(t *testing.T, server *Server, organizationID, code string, uses int) {
	t.Helper()
	_, err := server.config.Pool.Exec(t.Context(), `INSERT INTO enrollment_codes(code_hash,organization_id,expires_at,uses_remaining,source_type) VALUES($1,$2,$3,$4,'endpoint')`, tokenHash(normalizeCode(code)), organizationID, time.Now().Add(10*time.Minute), uses)
	if err != nil {
		t.Fatal(err)
	}
}

func exchangeTestIdentity(t *testing.T, server *Server, state identity.State, code, hostname string) (*httptest.ResponseRecorder, enrollmentResult) {
	t.Helper()
	proof, err := state.Sign(code, hostname, "darwin", "arm64", "2.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"code": code, "hostname": hostname, "platform": "darwin", "architecture": "arm64", "collector_version": "2.0.0-test", "identity_public_key": state.PublicKey, "identity_proof": proof})
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

func newIdentityTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	ctx, pool := integrationPool(t)
	organizationID := "identity-" + uuid.NewString()
	server, err := NewServer(ctx, Config{Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"), DevAdminToken: "identity-admin", DefaultOrganizationID: organizationID, PublicURL: "http://lens.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, organizationID) })
	return server, organizationID
}

func TestPersistentIdentityReenrollmentReusesTargetAndSource(t *testing.T) {
	server, orgID := newIdentityTestServer(t)
	state, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")
	if err != nil {
		t.Fatal(err)
	}
	createTestEnrollmentCode(t, server, orgID, "FIRST-CODE", 1)
	response, first := exchangeTestIdentity(t, server, state, "FIRST-CODE", "old-name.local")
	if response.Code != http.StatusOK {
		t.Fatalf("first enrollment returned %d: %s", response.Code, response.Body.String())
	}
	if _, err := server.config.Pool.Exec(t.Context(), `UPDATE sources SET last_sequence=17 WHERE organization_id=$1 AND id=$2`, orgID, first.SourceID); err != nil {
		t.Fatal(err)
	}
	createTestEnrollmentCode(t, server, orgID, "SECOND-CODE", 1)
	response, second := exchangeTestIdentity(t, server, state, "SECOND-CODE", "renamed.local")
	if response.Code != http.StatusOK {
		t.Fatalf("second enrollment returned %d: %s", response.Code, response.Body.String())
	}
	if first.TargetID != second.TargetID || first.SourceID != second.SourceID || second.Sequence != 17 || first.RefreshToken == second.RefreshToken {
		t.Fatalf("identity did not continue cleanly: first=%+v second=%+v", first, second)
	}
	var targets, sources, refreshTokens int
	var targetName string
	err = server.config.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM discovery_targets WHERE organization_id=$1),(SELECT count(*) FROM sources WHERE organization_id=$1 AND revoked_at IS NULL),(SELECT count(*) FROM collector_refresh_tokens WHERE organization_id=$1),(SELECT name FROM discovery_targets WHERE organization_id=$1 AND id=$2)`, orgID, first.TargetID).Scan(&targets, &sources, &refreshTokens, &targetName)
	if err != nil {
		t.Fatal(err)
	}
	if targets != 1 || sources != 1 || refreshTokens != 1 || targetName != "renamed.local" {
		t.Fatalf("unexpected enrollment state targets=%d sources=%d refresh=%d name=%q", targets, sources, refreshTokens, targetName)
	}
}

func TestSameHostnameDifferentIdentitiesRemainDistinct(t *testing.T) {
	server, orgID := newIdentityTestServer(t)
	firstIdentity, _ := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")
	secondIdentity, _ := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")
	createTestEnrollmentCode(t, server, orgID, "FIRST-HOST", 1)
	createTestEnrollmentCode(t, server, orgID, "SECOND-HOST", 1)
	firstResponse, first := exchangeTestIdentity(t, server, firstIdentity, "FIRST-HOST", "shared-name.local")
	secondResponse, second := exchangeTestIdentity(t, server, secondIdentity, "SECOND-HOST", "shared-name.local")
	if firstResponse.Code != 200 || secondResponse.Code != 200 || first.TargetID == second.TargetID || first.SourceID == second.SourceID {
		t.Fatalf("distinct endpoint identities were not preserved")
	}
	var duplicates int
	if err := server.config.Pool.QueryRow(t.Context(), `SELECT count(*) FROM discovery_targets t WHERE organization_id=$1 AND EXISTS(SELECT 1 FROM discovery_targets d WHERE d.organization_id=t.organization_id AND d.id<>t.id AND d.target_type=t.target_type AND lower(d.name)=lower(t.name))`, orgID).Scan(&duplicates); err != nil {
		t.Fatal(err)
	}
	if duplicates != 2 {
		t.Fatalf("expected both identities to be duplicate candidates, got %d", duplicates)
	}
}

func TestInvalidIdentityProofDoesNotConsumeEnrollmentCode(t *testing.T) {
	server, orgID := newIdentityTestServer(t)
	validIdentity, _ := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")
	otherIdentity, _ := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")
	createTestEnrollmentCode(t, server, orgID, "PROOF-CODE", 1)
	proof, _ := otherIdentity.Sign("PROOF-CODE", "device.local", "darwin", "arm64", "2.0.0-test")
	body, _ := json.Marshal(map[string]any{"code": "PROOF-CODE", "hostname": "device.local", "platform": "darwin", "architecture": "arm64", "collector_version": "2.0.0-test", "identity_public_key": validIdentity.PublicKey, "identity_proof": proof})
	request := httptest.NewRequest(http.MethodPost, "/v1/enrollment/exchange", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid proof returned %d", response.Code)
	}
	validResponse, _ := exchangeTestIdentity(t, server, validIdentity, "PROOF-CODE", "device.local")
	if validResponse.Code != http.StatusOK {
		t.Fatalf("valid proof could not use preserved enrollment code: %d", validResponse.Code)
	}
}

func TestConcurrentEnrollmentIsIdempotent(t *testing.T) {
	server, orgID := newIdentityTestServer(t)
	state, _ := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.json"), "http://lens.test")
	createTestEnrollmentCode(t, server, orgID, "RACE-CODE", 2)
	results := make([]enrollmentResult, 2)
	statuses := make([]int, 2)
	var group sync.WaitGroup
	for index := range 2 {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			response, result := exchangeTestIdentity(t, server, state, "RACE-CODE", "race.local")
			statuses[index], results[index] = response.Code, result
		}(index)
	}
	group.Wait()
	if statuses[0] != 200 || statuses[1] != 200 || results[0].TargetID != results[1].TargetID || results[0].SourceID != results[1].SourceID {
		t.Fatalf("concurrent enrollment was not idempotent: statuses=%v results=%+v", statuses, results)
	}
}
