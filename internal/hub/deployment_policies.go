package hub

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	defaultPolicyCredentialTTL = 24 * time.Hour
	maxPolicyCredentialTTL     = 30 * 24 * time.Hour
)

var sensitivePolicyConfigurationKey = regexp.MustCompile(`(?i)(secret|password|credential|token|private.?key|api.?key)`)

type deploymentPolicy struct {
	ID                         uuid.UUID          `json:"id"`
	Name                       string             `json:"name"`
	Description                *string            `json:"description,omitempty"`
	SourceType                 string             `json:"source_type"`
	DiscoveryDepth             string             `json:"discovery_depth"`
	Configuration              json.RawMessage    `json:"configuration"`
	ExpectedDeviceCount        *int               `json:"expected_device_count"`
	CredentialExpiresInSeconds int                `json:"credential_expires_in_seconds"`
	MaxEnrollments             int                `json:"max_enrollments"`
	Status                     string             `json:"status"`
	EnrolledCount              int                `json:"enrolled_count"`
	RemainingCount             int                `json:"remaining_count"`
	CreatedBy                  string             `json:"created_by"`
	UpdatedBy                  string             `json:"updated_by"`
	CreatedAt                  time.Time          `json:"created_at"`
	UpdatedAt                  time.Time          `json:"updated_at"`
	RevokedAt                  *time.Time         `json:"revoked_at,omitempty"`
	Credentials                []policyCredential `json:"credentials,omitempty"`
}

type policyCredential struct {
	ID             uuid.UUID  `json:"id"`
	Status         string     `json:"status"`
	ExpiresAt      time.Time  `json:"expires_at"`
	MaxEnrollments int        `json:"max_enrollments"`
	EnrolledCount  int        `json:"enrolled_count"`
	RemainingCount int        `json:"remaining_count"`
	CreatedBy      *string    `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
}

const deploymentPolicyColumns = `p.id,p.name,p.description,p.source_type,p.discovery_depth,p.configuration,
	p.expected_device_count,p.credential_ttl_seconds,p.max_enrollments,p.created_by,p.updated_by,
	p.revoked_at,p.created_at,p.updated_at,
	(SELECT count(DISTINCT e.target_id) FROM deployment_policy_enrollments e WHERE e.organization_id=p.organization_id AND e.policy_id=p.id),
	COALESCE((SELECT sum(c.uses_remaining) FROM enrollment_codes c WHERE c.organization_id=p.organization_id AND c.policy_id=p.id AND c.revoked_at IS NULL AND c.expires_at>now()),0)`

type policyRowScanner interface{ Scan(...any) error }

func scanDeploymentPolicy(row policyRowScanner) (deploymentPolicy, error) {
	var value deploymentPolicy
	err := row.Scan(&value.ID, &value.Name, &value.Description, &value.SourceType, &value.DiscoveryDepth, &value.Configuration,
		&value.ExpectedDeviceCount, &value.CredentialExpiresInSeconds, &value.MaxEnrollments, &value.CreatedBy, &value.UpdatedBy,
		&value.RevokedAt, &value.CreatedAt, &value.UpdatedAt, &value.EnrolledCount, &value.RemainingCount)
	if value.RevokedAt == nil {
		value.Status = "active"
	} else {
		value.Status = "revoked"
	}
	if value.Configuration == nil {
		value.Configuration = json.RawMessage(`{}`)
	}
	return value, err
}

func requirePolicyAdmin(r *http.Request) (Principal, bool) {
	principal, err := requireScope(r, "admin:enrollment")
	return principal, err == nil && principal.Admin
}

func validatePolicySourceType(value string) bool {
	return value == "endpoint" || value == "repository" || value == "kubernetes"
}

func validateDiscoveryDepth(value string) bool {
	return value == "basic" || value == "standard" || value == "deep"
}

func validatePolicyConfiguration(value map[string]any) bool {
	return validatePolicyConfigurationValue(value)
}

func validatePolicyConfigurationValue(value any) bool {
	switch nested := value.(type) {
	case map[string]any:
		for key, child := range nested {
			if sensitivePolicyConfigurationKey.MatchString(key) || !validatePolicyConfigurationValue(child) {
				return false
			}
		}
	case []any:
		for _, child := range nested {
			if !validatePolicyConfigurationValue(child) {
				return false
			}
		}
	}
	return true
}

func validatePolicyFields(name, sourceType, depth string, configuration map[string]any, expected *int, ttlSeconds, maxEnrollments int) (string, string) {
	if name = strings.TrimSpace(name); name == "" || len(name) > 128 {
		return "invalid_name", "Policy name is required and must be at most 128 characters"
	}
	if !validatePolicySourceType(sourceType) {
		return "invalid_source_type", "Source type must be endpoint, repository, or kubernetes"
	}
	if !validateDiscoveryDepth(depth) {
		return "invalid_discovery_depth", "Discovery depth must be basic, standard, or deep"
	}
	if !validatePolicyConfiguration(configuration) {
		return "secret_not_allowed", "Policy configuration must not contain credentials or secret values"
	}
	if expected != nil && *expected < 0 {
		return "invalid_expected_device_count", "Expected device count cannot be negative"
	}
	if ttlSeconds < 60 || ttlSeconds > int(maxPolicyCredentialTTL.Seconds()) {
		return "invalid_expiry", "Credential expiry must be between 60 seconds and 30 days"
	}
	if maxEnrollments < 1 || maxEnrollments > 100000 {
		return "invalid_max_enrollments", "Maximum enrollments must be between 1 and 100000"
	}
	return "", ""
}

func (s *Server) createDeploymentPolicy(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	var request struct {
		Name                       string         `json:"name"`
		Description                string         `json:"description"`
		SourceType                 string         `json:"source_type"`
		DiscoveryDepth             string         `json:"discovery_depth"`
		Configuration              map[string]any `json:"configuration"`
		ExpectedDeviceCount        *int           `json:"expected_device_count"`
		CredentialExpiresInSeconds int            `json:"credential_expires_in_seconds"`
		MaxEnrollments             int            `json:"max_enrollments"`
	}
	if err := decodeJSON(w, r, &request, 128<<10); err != nil {
		return
	}
	request.Name, request.Description = strings.TrimSpace(request.Name), strings.TrimSpace(request.Description)
	if request.SourceType == "" {
		request.SourceType = "endpoint"
	}
	if request.DiscoveryDepth == "" {
		request.DiscoveryDepth = "standard"
	}
	if request.Configuration == nil {
		request.Configuration = map[string]any{}
	}
	if request.CredentialExpiresInSeconds == 0 {
		request.CredentialExpiresInSeconds = int(defaultPolicyCredentialTTL.Seconds())
	}
	if request.MaxEnrollments == 0 {
		request.MaxEnrollments = 100
	}
	if code, message := validatePolicyFields(request.Name, request.SourceType, request.DiscoveryDepth, request.Configuration, request.ExpectedDeviceCount, request.CredentialExpiresInSeconds, request.MaxEnrollments); code != "" {
		writeError(w, http.StatusBadRequest, code, message)
		return
	}
	id := uuid.New()
	configuration := jsonBytes(request.Configuration)
	_, err := s.db(r.Context()).Exec(r.Context(), `INSERT INTO deployment_policies(id,organization_id,name,description,source_type,discovery_depth,configuration,expected_device_count,credential_ttl_seconds,max_enrollments,created_by,updated_by)
		VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$11)`, id, principal.OrganizationID, request.Name, request.Description, request.SourceType, request.DiscoveryDepth, configuration, request.ExpectedDeviceCount, request.CredentialExpiresInSeconds, request.MaxEnrollments, principal.Subject)
	if err == nil {
		_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'deployment_policy.created','deployment_policy',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, id.String(), jsonBytes(map[string]any{"source_type": request.SourceType, "discovery_depth": request.DiscoveryDepth, "max_enrollments": request.MaxEnrollments}))
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not create deployment policy")
		return
	}
	value, err := s.loadDeploymentPolicy(r, principal.OrganizationID, id, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not load deployment policy")
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (s *Server) listDeploymentPolicies(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	rows, err := s.db(r.Context()).Query(r.Context(), `SELECT `+deploymentPolicyColumns+` FROM deployment_policies p WHERE p.organization_id=$1 ORDER BY p.created_at DESC`, principal.OrganizationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not list deployment policies")
		return
	}
	defer rows.Close()
	values := make([]deploymentPolicy, 0)
	for rows.Next() {
		value, scanErr := scanDeploymentPolicy(rows)
		if scanErr != nil {
			writeError(w, http.StatusInternalServerError, "database_error", "Could not list deployment policies")
			return
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not list deployment policies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": values})
}

func (s *Server) getDeploymentPolicy(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Deployment policy ID is invalid")
		return
	}
	value, err := s.loadDeploymentPolicy(r, principal.OrganizationID, id, true)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Deployment policy not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not load deployment policy")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) loadDeploymentPolicy(r *http.Request, organizationID string, id uuid.UUID, includeCredentials bool) (deploymentPolicy, error) {
	value, err := scanDeploymentPolicy(s.db(r.Context()).QueryRow(r.Context(), `SELECT `+deploymentPolicyColumns+` FROM deployment_policies p WHERE p.organization_id=$1 AND p.id=$2`, organizationID, id))
	if err != nil || !includeCredentials {
		return value, err
	}
	rows, err := s.db(r.Context()).Query(r.Context(), `SELECT id,expires_at,max_uses,uses_remaining,created_by,created_at,revoked_at FROM enrollment_codes WHERE organization_id=$1 AND policy_id=$2 ORDER BY created_at DESC`, organizationID, id)
	if err != nil {
		return value, err
	}
	defer rows.Close()
	value.Credentials = make([]policyCredential, 0)
	for rows.Next() {
		var credential policyCredential
		var remaining int
		if err := rows.Scan(&credential.ID, &credential.ExpiresAt, &credential.MaxEnrollments, &remaining, &credential.CreatedBy, &credential.CreatedAt, &credential.RevokedAt); err != nil {
			return value, err
		}
		credential.EnrolledCount = credential.MaxEnrollments - remaining
		credential.RemainingCount = remaining
		credential.Status = credentialStatus(credential.RevokedAt, credential.ExpiresAt, remaining)
		value.Credentials = append(value.Credentials, credential)
	}
	return value, rows.Err()
}

func credentialStatus(revokedAt *time.Time, expiresAt time.Time, remaining int) string {
	if revokedAt != nil {
		return "revoked"
	}
	if !expiresAt.After(time.Now()) {
		return "expired"
	}
	if remaining == 0 {
		return "exhausted"
	}
	return "active"
}

func (s *Server) updateDeploymentPolicy(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Deployment policy ID is invalid")
		return
	}
	var request struct {
		Name                       *string         `json:"name"`
		Description                *string         `json:"description"`
		DiscoveryDepth             *string         `json:"discovery_depth"`
		Configuration              *map[string]any `json:"configuration"`
		ExpectedDeviceCount        json.RawMessage `json:"expected_device_count"`
		CredentialExpiresInSeconds *int            `json:"credential_expires_in_seconds"`
		MaxEnrollments             *int            `json:"max_enrollments"`
	}
	if err := decodeJSON(w, r, &request, 128<<10); err != nil {
		return
	}
	var current deploymentPolicy
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT id,name,description,source_type,discovery_depth,configuration,expected_device_count,credential_ttl_seconds,max_enrollments,revoked_at FROM deployment_policies WHERE organization_id=$1 AND id=$2 FOR UPDATE`, principal.OrganizationID, id).Scan(&current.ID, &current.Name, &current.Description, &current.SourceType, &current.DiscoveryDepth, &current.Configuration, &current.ExpectedDeviceCount, &current.CredentialExpiresInSeconds, &current.MaxEnrollments, &current.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Deployment policy not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not update deployment policy")
		return
	}
	if current.RevokedAt != nil {
		writeError(w, http.StatusConflict, "policy_revoked", "Revoked deployment policies cannot be updated")
		return
	}
	configuration := map[string]any{}
	_ = json.Unmarshal(current.Configuration, &configuration)
	if request.Name != nil {
		current.Name = strings.TrimSpace(*request.Name)
	}
	if request.Description != nil {
		value := strings.TrimSpace(*request.Description)
		current.Description = &value
	}
	if request.DiscoveryDepth != nil {
		current.DiscoveryDepth = *request.DiscoveryDepth
	}
	if request.Configuration != nil {
		configuration = *request.Configuration
	}
	if request.CredentialExpiresInSeconds != nil {
		current.CredentialExpiresInSeconds = *request.CredentialExpiresInSeconds
	}
	if request.MaxEnrollments != nil {
		current.MaxEnrollments = *request.MaxEnrollments
	}
	if len(request.ExpectedDeviceCount) > 0 {
		if string(request.ExpectedDeviceCount) == "null" {
			current.ExpectedDeviceCount = nil
		} else {
			var expected int
			if err := json.Unmarshal(request.ExpectedDeviceCount, &expected); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_expected_device_count", "Expected device count must be a non-negative integer or null")
				return
			}
			current.ExpectedDeviceCount = &expected
		}
	}
	if code, message := validatePolicyFields(current.Name, current.SourceType, current.DiscoveryDepth, configuration, current.ExpectedDeviceCount, current.CredentialExpiresInSeconds, current.MaxEnrollments); code != "" {
		writeError(w, http.StatusBadRequest, code, message)
		return
	}
	description := ""
	if current.Description != nil {
		description = *current.Description
	}
	_, err = s.db(r.Context()).Exec(r.Context(), `UPDATE deployment_policies SET name=$3,description=NULLIF($4,''),discovery_depth=$5,configuration=$6,expected_device_count=$7,credential_ttl_seconds=$8,max_enrollments=$9,updated_by=$10,updated_at=now() WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, id, current.Name, description, current.DiscoveryDepth, jsonBytes(configuration), current.ExpectedDeviceCount, current.CredentialExpiresInSeconds, current.MaxEnrollments, principal.Subject)
	if err == nil {
		_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'deployment_policy.updated','deployment_policy',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, id.String(), jsonBytes(map[string]any{"discovery_depth": current.DiscoveryDepth, "max_enrollments": current.MaxEnrollments}))
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not update deployment policy")
		return
	}
	value, err := s.loadDeploymentPolicy(r, principal.OrganizationID, id, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not load deployment policy")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) revokeDeploymentPolicy(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Deployment policy ID is invalid")
		return
	}
	result, err := s.db(r.Context()).Exec(r.Context(), `UPDATE deployment_policies SET revoked_at=now(),updated_by=$3,updated_at=now() WHERE organization_id=$1 AND id=$2 AND revoked_at IS NULL`, principal.OrganizationID, id, principal.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not revoke deployment policy")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Active deployment policy not found")
		return
	}
	credentials, err := s.db(r.Context()).Exec(r.Context(), `UPDATE enrollment_codes SET revoked_at=now(),revoked_by=$3 WHERE organization_id=$1 AND policy_id=$2 AND revoked_at IS NULL`, principal.OrganizationID, id, principal.Subject)
	if err == nil && credentials.RowsAffected() > 0 {
		_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'deployment_policy.credential_revoked','deployment_policy',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, id.String(), jsonBytes(map[string]any{"reason": "policy_revoked", "credential_count": credentials.RowsAffected()}))
	}
	if err == nil {
		_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'deployment_policy.revoked','deployment_policy',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, id.String(), jsonBytes(map[string]any{"credentials_revoked": credentials.RowsAffected()}))
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not revoke deployment policy")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateDeploymentPolicyCredential(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	policyID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Deployment policy ID is invalid")
		return
	}
	var request struct {
		ExpiresInSeconds int `json:"expires_in_seconds"`
		MaxEnrollments   int `json:"max_enrollments"`
	}
	if err := decodeJSON(w, r, &request, 16<<10); err != nil {
		return
	}
	var sourceType string
	var defaultTTL, defaultMax int
	var revokedAt *time.Time
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT source_type,credential_ttl_seconds,max_enrollments,revoked_at FROM deployment_policies WHERE organization_id=$1 AND id=$2 FOR UPDATE`, principal.OrganizationID, policyID).Scan(&sourceType, &defaultTTL, &defaultMax, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Deployment policy not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not issue enrollment credential")
		return
	}
	if revokedAt != nil {
		writeError(w, http.StatusConflict, "policy_revoked", "Revoked deployment policies cannot issue credentials")
		return
	}
	if request.ExpiresInSeconds == 0 {
		request.ExpiresInSeconds = defaultTTL
	}
	if request.MaxEnrollments == 0 {
		request.MaxEnrollments = defaultMax
	}
	if request.ExpiresInSeconds < 60 || request.ExpiresInSeconds > int(maxPolicyCredentialTTL.Seconds()) {
		writeError(w, http.StatusBadRequest, "invalid_expiry", "Credential expiry must be between 60 seconds and 30 days")
		return
	}
	if request.MaxEnrollments < 1 || request.MaxEnrollments > 100000 {
		writeError(w, http.StatusBadRequest, "invalid_max_enrollments", "Maximum enrollments must be between 1 and 100000")
		return
	}
	code, err := enrollmentCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not issue enrollment credential")
		return
	}
	rotated, err := s.db(r.Context()).Exec(r.Context(), `UPDATE enrollment_codes SET revoked_at=now(),revoked_by=$3 WHERE organization_id=$1 AND policy_id=$2 AND revoked_at IS NULL`, principal.OrganizationID, policyID, principal.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not rotate enrollment credential")
		return
	}
	credentialID := uuid.New()
	expiresAt := time.Now().UTC().Add(time.Duration(request.ExpiresInSeconds) * time.Second)
	_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO enrollment_codes(id,code_hash,organization_id,policy_id,expires_at,uses_remaining,max_uses,source_type,created_by) VALUES($1,$2,$3,$4,$5,$6,$6,$7,$8)`, credentialID, tokenHash(normalizeCode(code)), principal.OrganizationID, policyID, expiresAt, request.MaxEnrollments, sourceType, principal.Subject)
	if err == nil && rotated.RowsAffected() > 0 {
		_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'deployment_policy.credential_revoked','deployment_policy',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, policyID.String(), jsonBytes(map[string]any{"reason": "rotation", "credential_count": rotated.RowsAffected()}))
	}
	if err == nil {
		_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'deployment_policy.credential_issued','enrollment_credential',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, credentialID.String(), jsonBytes(map[string]any{"policy_id": policyID.String(), "source_type": sourceType, "max_enrollments": request.MaxEnrollments, "expires_at": expiresAt}))
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not issue enrollment credential")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"credential": policyCredential{ID: credentialID, Status: "active", ExpiresAt: expiresAt, MaxEnrollments: request.MaxEnrollments, RemainingCount: request.MaxEnrollments, CreatedBy: &principal.Subject, CreatedAt: time.Now().UTC()},
		"code":       code, "credential_displayed_once": true, "hub_url": s.config.PublicURL, "collector_version": Version,
	})
}

func (s *Server) revokeDeploymentPolicyCredential(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	policyID, policyErr := uuid.Parse(r.PathValue("id"))
	credentialID, credentialErr := uuid.Parse(r.PathValue("credentialId"))
	if policyErr != nil || credentialErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Deployment policy or credential ID is invalid")
		return
	}
	result, err := s.db(r.Context()).Exec(r.Context(), `UPDATE enrollment_codes SET revoked_at=now(),revoked_by=$4 WHERE organization_id=$1 AND policy_id=$2 AND id=$3 AND revoked_at IS NULL`, principal.OrganizationID, policyID, credentialID, principal.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not revoke enrollment credential")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Active enrollment credential not found")
		return
	}
	_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'deployment_policy.credential_revoked','enrollment_credential',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, credentialID.String(), jsonBytes(map[string]any{"policy_id": policyID.String(), "reason": "manual"}))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not revoke enrollment credential")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
