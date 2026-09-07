package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/cloud"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	awsAccountPattern       = regexp.MustCompile(`^[0-9]{12}$`)
	gcpProjectPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	repositoryPattern       = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	gcpProjectNumberPattern = regexp.MustCompile(`^[0-9]{6,20}$`)
	sensitiveSetupKey       = regexp.MustCompile(`(?i)(secret|password|private.?key|access.?token|api.?key)`)
)

type environmentConnection struct {
	ID               uuid.UUID       `json:"id"`
	Kind             string          `json:"kind"`
	Provider         *string         `json:"provider,omitempty"`
	ExternalID       *string         `json:"external_id,omitempty"`
	DisplayName      string          `json:"display_name"`
	ConnectionStatus string          `json:"connection_status"`
	Configuration    json.RawMessage `json:"configuration"`
	TargetID         *string         `json:"target_id,omitempty"`
	SourceID         *string         `json:"source_id,omitempty"`
	ScheduleEnabled  bool            `json:"schedule_enabled"`
	NextScanAt       *time.Time      `json:"next_scan_at,omitempty"`
	VerifiedAt       *time.Time      `json:"verified_at,omitempty"`
	DisconnectedAt   *time.Time      `json:"disconnected_at,omitempty"`
	PurgeAfter       *time.Time      `json:"purge_after,omitempty"`
	LastErrorCode    *string         `json:"last_error_code,omitempty"`
	LastErrorMessage *string         `json:"last_error_message,omitempty"`
	FirstResultAt    *time.Time      `json:"first_result_at,omitempty"`
	LastResultAt     *time.Time      `json:"last_result_at,omitempty"`
	LastResultStatus *string         `json:"last_result_status,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

const environmentColumns = `id,kind,provider,external_id,display_name,connection_status,configuration,target_id,source_id,schedule_enabled,next_scan_at,verified_at,disconnected_at,purge_after,last_error_code,last_error_message,first_result_at,last_result_at,last_result_status,created_at,updated_at`

type environmentRowScanner interface{ Scan(...any) error }

func scanEnvironment(row environmentRowScanner) (environmentConnection, error) {
	var value environmentConnection
	err := row.Scan(&value.ID, &value.Kind, &value.Provider, &value.ExternalID, &value.DisplayName, &value.ConnectionStatus, &value.Configuration, &value.TargetID, &value.SourceID, &value.ScheduleEnabled, &value.NextScanAt, &value.VerifiedAt, &value.DisconnectedAt, &value.PurgeAfter, &value.LastErrorCode, &value.LastErrorMessage, &value.FirstResultAt, &value.LastResultAt, &value.LastResultStatus, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	var name string
	err := s.db(r.Context()).QueryRow(r.Context(), `SELECT name FROM organizations WHERE id=$1`, principal.OrganizationID).Scan(&name)
	needsBootstrap := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !needsBootstrap {
		writeError(w, 500, "database_error", "Could not load the active workspace")
		return
	}
	role := principal.Role
	if role == "" {
		if principal.Admin {
			role = "owner"
		} else {
			role = "viewer"
		}
	}
	permissions := make([]string, 0, len(principal.Scopes))
	for scope, allowed := range principal.Scopes {
		if allowed {
			permissions = append(permissions, scope)
		}
	}
	var ownerCount int
	if !needsBootstrap {
		_ = s.db(r.Context()).QueryRow(r.Context(), `SELECT count(*) FROM workspace_memberships WHERE organization_id=$1 AND role='owner' AND status='active'`, principal.OrganizationID).Scan(&ownerCount)
	}
	writeJSON(w, 200, map[string]any{
		"user":      map[string]any{"id": principal.Subject},
		"workspace": map[string]any{"id": principal.OrganizationID, "name": name},
		"role":      role, "permissions": permissions, "needs_bootstrap": needsBootstrap,
		"can_delete_account": role != "owner" || ownerCount > 1,
	})
}

func (s *Server) bootstrapWorkspace(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		writeError(w, 400, "invalid_name", "Workspace name is required")
		return
	}
	if len(request.Name) > 128 {
		writeError(w, 400, "invalid_name", "Workspace name must be at most 128 characters")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not provision the workspace")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `INSERT INTO organizations(id,name) VALUES($1,$2) ON CONFLICT(id) DO NOTHING`, principal.OrganizationID, request.Name)
	created := tag.RowsAffected() == 1
	role := normalizeWorkspaceRole(principal.Role)
	var ownerCount int
	if err == nil {
		err = tx.QueryRow(r.Context(), `SELECT count(*) FROM workspace_memberships WHERE organization_id=$1 AND role='owner' AND status='active'`, principal.OrganizationID).Scan(&ownerCount)
	}
	if ownerCount == 0 {
		role = "owner"
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO workspace_memberships(organization_id,user_id,role,status) VALUES($1,$2,$3,'active') ON CONFLICT(organization_id,user_id) DO UPDATE SET role=CASE WHEN workspace_memberships.role='owner' THEN 'owner' ELSE EXCLUDED.role END,status='active',updated_at=now()`, principal.OrganizationID, principal.Subject, role)
	}
	if err == nil && created {
		_, err = tx.Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id) VALUES($1,$2,$3,'workspace.created','workspace',$2)`, uuid.New(), principal.OrganizationID, principal.Subject)
	}
	if err == nil && created {
		_, err = tx.Exec(r.Context(), `INSERT INTO product_events(id,organization_id,actor_id,event_type) VALUES($1,$2,$3,'workspace_created')`, uuid.New(), principal.OrganizationID, principal.Subject)
	}
	if err == nil && created {
		_, err = tx.Exec(r.Context(), `INSERT INTO product_events(id,organization_id,actor_id,event_type) VALUES($1,$2,$3,'signup_completed') ON CONFLICT DO NOTHING`, uuid.New(), principal.OrganizationID, principal.Subject)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 500, "database_error", "Could not provision the workspace")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"id": principal.OrganizationID, "name": request.Name, "role": role, "created": created})
}

func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "workspace:delete")
	if err != nil || principal.Role != "owner" && !principal.Scopes["*"] {
		writeError(w, 403, "forbidden", "Workspace owner access is required")
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(w, r, &request, 16<<10); err != nil {
		return
	}
	if request.Confirmation != principal.OrganizationID {
		writeError(w, 400, "confirmation_required", "Enter the workspace ID to confirm immediate deletion")
		return
	}
	tag, err := s.db(r.Context()).Exec(r.Context(), `DELETE FROM organizations WHERE id=$1`, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not delete the workspace")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "Workspace not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok || !strings.HasPrefix(principal.Subject, "clerk:") {
		writeError(w, http.StatusBadRequest, "managed_account_required", "Account deletion is available for Clerk-managed identities")
		return
	}
	if principal.Role == "owner" {
		var owners int
		if err := s.db(r.Context()).QueryRow(r.Context(), `SELECT count(*) FROM workspace_memberships WHERE organization_id=$1 AND role='owner' AND status='active'`, principal.OrganizationID).Scan(&owners); err != nil {
			writeError(w, 500, "database_error", "Could not validate workspace ownership")
			return
		}
		if owners <= 1 {
			writeError(w, http.StatusConflict, "sole_owner", "Transfer ownership or delete the workspace before deleting this account")
			return
		}
	}
	clerkUserID := strings.TrimPrefix(principal.Subject, "clerk:")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(s.config.ClerkAPIBaseURL, "/")+"/v1/users/"+url.PathEscape(clerkUserID), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "identity_delete_failed", "Could not prepare identity deletion")
		return
	}
	request.Header.Set("Authorization", "Bearer "+s.config.ClerkSecretKey)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil || response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		if response != nil {
			response.Body.Close()
		}
		writeError(w, http.StatusServiceUnavailable, "identity_provider_unavailable", "The identity provider could not delete this account")
		return
	}
	response.Body.Close()
	if _, err := s.db(r.Context()).Exec(r.Context(), `UPDATE workspace_memberships SET status='deleted',updated_at=now() WHERE organization_id=$1 AND user_id=$2`, principal.OrganizationID, principal.Subject); err != nil {
		writeError(w, 500, "database_error", "Could not delete the account")
		return
	}
	if _, err := s.db(r.Context()).Exec(r.Context(), `INSERT INTO managed_users(user_id,status,updated_at) VALUES($1,'deleted',now()) ON CONFLICT(user_id) DO UPDATE SET status='deleted',updated_at=now()`, principal.Subject); err != nil {
		writeError(w, 500, "database_error", "Could not delete the account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	rows, err := s.db(r.Context()).Query(r.Context(), `SELECT `+environmentColumns+` FROM environment_connections WHERE organization_id=$1 ORDER BY created_at DESC`, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not list environments")
		return
	}
	defer rows.Close()
	items := []environmentConnection{}
	for rows.Next() {
		value, scanErr := scanEnvironment(rows)
		if scanErr != nil {
			writeError(w, 500, "database_error", "Could not list environments")
			return
		}
		items = append(items, value)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) getEnvironment(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Environment ID is invalid")
		return
	}
	value, err := scanEnvironment(s.db(r.Context()).QueryRow(r.Context(), `SELECT `+environmentColumns+` FROM environment_connections WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Environment not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not load the environment")
		return
	}
	writeJSON(w, 200, value)
}

func (s *Server) createEnvironmentSetupSession(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	if !s.config.SelfServeEnabled {
		writeError(w, 404, "self_serve_disabled", "Self-serve environment onboarding is disabled")
		return
	}
	var request struct {
		Kind          string         `json:"kind"`
		DisplayName   string         `json:"display_name"`
		ExternalID    string         `json:"external_id"`
		Configuration map[string]any `json:"configuration"`
	}
	if err := decodeJSON(w, r, &request, 128<<10); err != nil {
		return
	}
	provider, externalID, err := s.validateEnvironmentSetup(request.Kind, request.DisplayName, request.ExternalID, request.Configuration)
	if err != nil {
		writeError(w, 400, "invalid_environment", err.Error())
		return
	}
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	if request.DisplayName == "" {
		request.DisplayName = externalID
	}
	if request.Configuration == nil {
		request.Configuration = map[string]any{}
	}
	for key := range request.Configuration {
		if sensitiveSetupKey.MatchString(key) {
			writeError(w, 400, "secret_not_allowed", "Setup configuration must not contain credentials or secret values")
			return
		}
	}
	if provider == "aws" {
		externalBytes := make([]byte, 16)
		if _, err := rand.Read(externalBytes); err != nil {
			writeError(w, 500, "internal_error", "Could not prepare setup")
			return
		}
		request.Configuration["external_id"] = hex.EncodeToString(externalBytes)
	}
	if provider == "gcp" {
		projectNumber, _ := request.Configuration["project_number"].(string)
		request.Configuration["workload_identity_audience"] = fmt.Sprintf("//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/barrikade-lens/providers/lens-azure", projectNumber)
	}
	configuration, _ := json.Marshal(request.Configuration)
	token, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not prepare setup")
		return
	}
	environmentID, setupID := uuid.New(), uuid.New()
	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not prepare setup")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO environment_connections(id,organization_id,kind,provider,external_id,display_name,configuration,created_by) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8)`, environmentID, principal.OrganizationID, request.Kind, provider, externalID, request.DisplayName, configuration, principal.Subject)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO connector_setup_sessions(id,organization_id,environment_id,token_hash,kind,setup_payload,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, setupID, principal.OrganizationID, environmentID, tokenHash(normalizeCode(token)), request.Kind, configuration, principal.Subject, expiresAt)
	}
	if err == nil && (request.Kind == "endpoint" || request.Kind == "kubernetes_cluster") {
		sourceType := "endpoint"
		if request.Kind == "kubernetes_cluster" {
			sourceType = "kubernetes"
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO enrollment_codes(code_hash,organization_id,environment_id,expires_at,uses_remaining,source_type) VALUES($1,$2,$3,$4,1,$5)`, tokenHash(normalizeCode(token)), principal.OrganizationID, environmentID, expiresAt, sourceType)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'environment.setup_started','environment',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, environmentID.String(), jsonBytes(map[string]any{"kind": request.Kind, "provider": provider}))
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO product_events(id,organization_id,actor_id,event_type,properties) VALUES($1,$2,$3,'setup_started',$4)`, uuid.New(), principal.OrganizationID, principal.Subject, jsonBytes(map[string]any{"kind": request.Kind, "provider": provider}))
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "environment_exists", "This environment is already connected or being set up")
		} else {
			writeError(w, 500, "database_error", "Could not prepare setup")
		}
		return
	}
	setup := s.environmentSetupInstructions(request.Kind, provider, externalID, request.DisplayName, token, request.Configuration)
	writeJSON(w, http.StatusCreated, map[string]any{"id": setupID, "environment_id": environmentID, "kind": request.Kind, "expires_at": expiresAt, "setup": setup, "token_displayed_once": true})
}

func (s *Server) validateEnvironmentSetup(kind, displayName, externalID string, configuration map[string]any) (string, string, error) {
	kind, displayName, externalID = strings.TrimSpace(kind), strings.TrimSpace(displayName), strings.TrimSpace(externalID)
	if len(displayName) > 128 || len(externalID) > 256 {
		return "", "", fmt.Errorf("environment name or identifier is too long")
	}
	switch kind {
	case "aws_account":
		if !s.config.AWSConnectorEnabled {
			return "", "", fmt.Errorf("the AWS connector is not enabled")
		}
		if !awsAccountPattern.MatchString(externalID) {
			return "", "", fmt.Errorf("AWS account ID must contain 12 digits")
		}
		return "aws", externalID, nil
	case "azure_subscription":
		if !s.config.AzureConnectorEnabled {
			return "", "", fmt.Errorf("the Azure connector is not enabled")
		}
		if _, err := uuid.Parse(externalID); err != nil {
			return "", "", fmt.Errorf("Azure subscription ID must be a UUID")
		}
		tenantID, _ := configuration["tenant_id"].(string)
		if _, err := uuid.Parse(strings.TrimSpace(tenantID)); err != nil {
			return "", "", fmt.Errorf("Microsoft Entra tenant ID must be a UUID")
		}
		return "azure", strings.ToLower(externalID), nil
	case "gcp_project":
		if !s.config.GCPConnectorEnabled {
			return "", "", fmt.Errorf("the GCP connector is not enabled")
		}
		if !gcpProjectPattern.MatchString(externalID) {
			return "", "", fmt.Errorf("GCP project ID is invalid")
		}
		projectNumber, _ := configuration["project_number"].(string)
		if !gcpProjectNumberPattern.MatchString(strings.TrimSpace(projectNumber)) {
			return "", "", fmt.Errorf("GCP project number must contain 6 to 20 digits")
		}
		return "gcp", externalID, nil
	case "endpoint":
		if !s.config.EndpointConnectorEnabled {
			return "", "", fmt.Errorf("the endpoint connector is not enabled")
		}
		return "endpoint", externalID, nil
	case "github_repository":
		if !s.config.GitHubConnectorEnabled || s.config.GitHubClient == nil {
			return "", "", fmt.Errorf("the GitHub connector is not enabled")
		}
		if externalID != "" && !repositoryPattern.MatchString(externalID) {
			return "", "", fmt.Errorf("repository must be owner/name")
		}
		return "github", strings.ToLower(externalID), nil
	case "kubernetes_cluster":
		if !s.config.KubernetesConnectorEnabled {
			return "", "", fmt.Errorf("the Kubernetes connector is not enabled")
		}
		return "kubernetes", externalID, nil
	default:
		return "", "", fmt.Errorf("unsupported environment kind")
	}
}

func (s *Server) verifyEnvironment(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Environment ID is invalid")
		return
	}
	var setupValid bool
	if err := s.db(r.Context()).QueryRow(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM connector_setup_sessions
		WHERE organization_id=$1 AND environment_id=$2 AND state='pending' AND expires_at>now()
	)`, principal.OrganizationID, id).Scan(&setupValid); err != nil {
		writeError(w, 500, "database_error", "Could not validate setup")
		return
	}
	if !setupValid {
		writeError(w, 410, "setup_expired", "Setup has expired or was cancelled; rotate the setup credential to continue")
		return
	}
	value, err := scanEnvironment(s.db(r.Context()).QueryRow(r.Context(), `UPDATE environment_connections SET connection_status='verifying',updated_at=now() WHERE organization_id=$1 AND id=$2 AND connection_status IN ('setup_pending','auth_error') RETURNING `+environmentColumns, principal.OrganizationID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "not_verifiable", "Environment is already connected, disconnected, or does not exist")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not start verification")
		return
	}
	if value.Provider == nil || (*value.Provider != "aws" && *value.Provider != "azure" && *value.Provider != "gcp") {
		_, _ = s.db(r.Context()).Exec(r.Context(), `UPDATE environment_connections SET connection_status='setup_pending',updated_at=now() WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, id)
		writeJSON(w, 202, map[string]any{"environment": value, "message": "Waiting for the collector or provider callback to complete enrollment"})
		return
	}
	adapter, err := s.config.CloudAdapters.Adapter(*value.Provider)
	if err != nil {
		if persistErr := s.failEnvironmentVerification(r.Context(), principal.OrganizationID, id, err); persistErr == nil {
			commitOnError(r.Context())
		}
		writeCloudError(w, err)
		return
	}
	cloudEnvironment := environmentForAdapter(principal.OrganizationID, value)
	verification, err := adapter.Verify(r.Context(), cloudEnvironment)
	if err != nil {
		if persistErr := s.failEnvironmentVerification(r.Context(), principal.OrganizationID, id, err); persistErr == nil {
			commitOnError(r.Context())
		}
		writeCloudError(w, err)
		return
	}
	targetID := discovery.StableID(principal.OrganizationID, discovery.KindCloudEnvironment, *value.Provider+":"+valueOrEmpty(value.ExternalID))
	sourceID := "source:cloud:" + id.String()
	nextScan := nextDailyScan(id, time.Now().UTC())
	tx, err := s.begin(r.Context())
	if err == nil {
		defer tx.Rollback(r.Context())
		_, err = tx.Exec(r.Context(), `INSERT INTO discovery_targets(organization_id,id,target_type,identity_quality,name,platform,current) VALUES($1,$2,'cloud','persistent',$3,$4,true) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,platform=EXCLUDED.platform,current=true`, principal.OrganizationID, targetID, value.DisplayName, *value.Provider)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO sources(organization_id,id,target_id,source_type,name,platform,collector_version) VALUES($1,$2,$3,'cloud',$4,$5,$6) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,platform=EXCLUDED.platform,revoked_at=NULL`, principal.OrganizationID, sourceID, targetID, value.DisplayName, *value.Provider, Version)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE environment_connections SET connection_status='connected',target_id=$3,source_id=$4,verified_at=now(),next_scan_at=$5,last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, id, targetID, sourceID, nextScan)
	}
	scanID := uuid.New()
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO cloud_scan_jobs(id,organization_id,environment_id,trigger,status,requested_by) VALUES($1,$2,$3,'first_scan','queued',$4)`, scanID, principal.OrganizationID, id, principal.Subject)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE connector_setup_sessions SET state='consumed',consumed_at=now() WHERE organization_id=$1 AND environment_id=$2 AND state='pending'`, principal.OrganizationID, id)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'environment.verified','environment',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, id.String(), jsonBytes(map[string]any{"provider": *value.Provider, "principal": verification.Principal}))
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO product_events(id,organization_id,actor_id,event_type,properties) VALUES($1,$2,$3,'environment_verified',$4),($5,$2,$3,'first_scan_started',$4)`, uuid.New(), principal.OrganizationID, principal.Subject, jsonBytes(map[string]any{"kind": value.Kind, "provider": *value.Provider}), uuid.New())
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 500, "database_error", "Access was verified, but Lens could not activate the environment")
		return
	}
	writeJSON(w, 200, map[string]any{"environment_id": id, "connection_status": "connected", "verification": verification, "scan": map[string]any{"id": scanID, "status": "queued"}})
}

func (s *Server) failEnvironmentVerification(ctx context.Context, organizationID string, environmentID uuid.UUID, err error) error {
	code, message, _, _ := cloud.SafeError(err)
	_, persistErr := s.db(ctx).Exec(ctx, `UPDATE environment_connections SET connection_status='auth_error',last_error_code=$3,last_error_message=$4,updated_at=now() WHERE organization_id=$1 AND id=$2`, organizationID, environmentID, code, message)
	return persistErr
}

func (s *Server) updateEnvironment(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Environment ID is invalid")
		return
	}
	var request struct {
		DisplayName     *string `json:"display_name"`
		ScheduleEnabled *bool   `json:"daily_schedule_enabled"`
	}
	if err := decodeJSON(w, r, &request, 32<<10); err != nil {
		return
	}
	if request.DisplayName == nil && request.ScheduleEnabled == nil {
		writeError(w, 400, "invalid_request", "Display name or daily schedule is required")
		return
	}
	name := ""
	if request.DisplayName != nil {
		name = strings.TrimSpace(*request.DisplayName)
		if name == "" || len(name) > 128 {
			writeError(w, 400, "invalid_name", "Display name must be 1 to 128 characters")
			return
		}
	}
	value, err := scanEnvironment(s.db(r.Context()).QueryRow(r.Context(), `UPDATE environment_connections SET display_name=CASE WHEN $3='' THEN display_name ELSE $3 END,schedule_enabled=COALESCE($4,schedule_enabled),next_scan_at=CASE WHEN COALESCE($4,schedule_enabled) THEN COALESCE(next_scan_at,now()) ELSE NULL END,updated_at=now() WHERE organization_id=$1 AND id=$2 AND connection_status<>'disconnected' RETURNING `+environmentColumns, principal.OrganizationID, id, name, request.ScheduleEnabled))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Active environment not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not update the environment")
		return
	}
	writeJSON(w, 200, value)
}

func (s *Server) createEnvironmentScan(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "scan:run")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	environmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Environment ID is invalid")
		return
	}
	var provider, status string
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT provider,connection_status FROM environment_connections WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, environmentID).Scan(&provider, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Environment not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not load the environment")
		return
	}
	if status != "connected" {
		writeError(w, 409, "environment_not_connected", "Connect and verify the environment before scanning")
		return
	}
	if provider != "aws" && provider != "azure" && provider != "gcp" {
		writeError(w, 409, "collector_managed", "This environment is refreshed by its installed collector")
		return
	}
	var activeID uuid.UUID
	var activeStatus string
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT id,status FROM cloud_scan_jobs WHERE organization_id=$1 AND environment_id=$2 AND status IN ('queued','running','ingesting') ORDER BY created_at LIMIT 1`, principal.OrganizationID, environmentID).Scan(&activeID, &activeStatus)
	if err == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"id": activeID, "status": activeStatus, "coalesced": true})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "database_error", "Could not request a scan")
		return
	}
	jobID := uuid.New()
	_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO cloud_scan_jobs(id,organization_id,environment_id,trigger,status,requested_by) VALUES($1,$2,$3,'manual','queued',$4)`, jobID, principal.OrganizationID, environmentID, principal.Subject)
	if err != nil {
		if isUniqueViolation(err) {
			_ = s.db(r.Context()).QueryRow(r.Context(), `SELECT id,status FROM cloud_scan_jobs WHERE organization_id=$1 AND environment_id=$2 AND status IN ('queued','running','ingesting') ORDER BY created_at LIMIT 1`, principal.OrganizationID, environmentID).Scan(&activeID, &activeStatus)
			writeJSON(w, http.StatusAccepted, map[string]any{"id": activeID, "status": activeStatus, "coalesced": true})
			return
		}
		writeError(w, 500, "database_error", "Could not request a scan")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": jobID, "status": "queued", "coalesced": false})
}

func (s *Server) getEnvironmentScan(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "jobs:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	environmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Environment ID is invalid")
		return
	}
	scanID, err := uuid.Parse(r.PathValue("scanId"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Scan ID is invalid")
		return
	}
	var status, phase, trigger string
	var progress []byte
	var errorCode, errorMessage *string
	var createdAt time.Time
	var startedAt, completedAt *time.Time
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT status,phase,trigger,progress,error_code,error_message,created_at,started_at,completed_at FROM cloud_scan_jobs WHERE organization_id=$1 AND environment_id=$2 AND id=$3`, principal.OrganizationID, environmentID, scanID).Scan(&status, &phase, &trigger, &progress, &errorCode, &errorMessage, &createdAt, &startedAt, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Scan not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not load the scan")
		return
	}
	writeJSON(w, 200, map[string]any{"id": scanID, "environment_id": environmentID, "status": status, "phase": phase, "trigger": trigger, "progress": jsonObject(progress), "safe_error": map[string]any{"code": errorCode, "message": errorMessage}, "created_at": createdAt, "started_at": startedAt, "completed_at": completedAt})
}

func (s *Server) disconnectEnvironment(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	environmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Environment ID is invalid")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not disconnect the environment")
		return
	}
	defer tx.Rollback(r.Context())
	var provider string
	var sourceID, targetID *string
	err = tx.QueryRow(r.Context(), `UPDATE environment_connections SET connection_status='disconnected',schedule_enabled=false,next_scan_at=NULL,disconnected_at=now(),purge_after=now()+interval '90 days',updated_at=now() WHERE organization_id=$1 AND id=$2 AND connection_status<>'disconnected' RETURNING provider,source_id,target_id`, principal.OrganizationID, environmentID).Scan(&provider, &sourceID, &targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Active environment not found")
		return
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE connector_setup_sessions SET state='cancelled' WHERE organization_id=$1 AND environment_id=$2 AND state='pending'`, principal.OrganizationID, environmentID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE enrollment_codes SET revoked_at=now() WHERE organization_id=$1 AND environment_id=$2 AND revoked_at IS NULL`, principal.OrganizationID, environmentID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE endpoint_setup_handoffs SET revoked_at=now() WHERE organization_id=$1 AND environment_id=$2 AND revoked_at IS NULL`, principal.OrganizationID, environmentID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE cloud_scan_jobs SET status='cancelled',phase='cancelled',completed_at=now() WHERE organization_id=$1 AND environment_id=$2 AND status IN ('queued','running','ingesting')`, principal.OrganizationID, environmentID)
	}
	if err == nil && sourceID != nil {
		err = retireSourceObservations(r.Context(), tx, principal.OrganizationID, *sourceID)
	}
	if err == nil && sourceID != nil {
		_, err = tx.Exec(r.Context(), `UPDATE sources SET revoked_at=now() WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, *sourceID)
	}
	if err == nil && sourceID != nil {
		_, err = tx.Exec(r.Context(), `DELETE FROM collector_refresh_tokens WHERE organization_id=$1 AND source_id=$2`, principal.OrganizationID, *sourceID)
	}
	if err == nil && targetID != nil {
		_, err = tx.Exec(r.Context(), `UPDATE discovery_targets SET current=false WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, *targetID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'environment.disconnected','environment',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, environmentID.String(), jsonBytes(map[string]any{"provider": provider}))
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO product_events(id,organization_id,actor_id,event_type,properties) VALUES($1,$2,$3,'disconnect',$4)`, uuid.New(), principal.OrganizationID, principal.Subject, jsonBytes(map[string]any{"provider": provider}))
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 500, "database_error", "Could not disconnect the environment")
		return
	}
	writeJSON(w, 200, map[string]any{"id": environmentID, "connection_status": "disconnected", "purge_after_days": 90, "teardown": teardownInstructions(provider)})
}

// retireSourceObservations removes a disconnected source from the current
// projection while retaining its observations for the environment's 90-day
// historical window.
func retireSourceObservations(ctx context.Context, tx pgx.Tx, organizationID, sourceID string) error {
	entityRows, err := tx.Query(ctx, `SELECT entity_id FROM source_entities WHERE organization_id=$1 AND source_id=$2 AND current=true`, organizationID, sourceID)
	if err != nil {
		return err
	}
	entityIDs := []string{}
	for entityRows.Next() {
		var id string
		if err := entityRows.Scan(&id); err != nil {
			entityRows.Close()
			return err
		}
		entityIDs = append(entityIDs, id)
	}
	if err := entityRows.Err(); err != nil {
		entityRows.Close()
		return err
	}
	entityRows.Close()

	relationRows, err := tx.Query(ctx, `SELECT relationship_id FROM source_relationships WHERE organization_id=$1 AND source_id=$2 AND current=true`, organizationID, sourceID)
	if err != nil {
		return err
	}
	relationIDs := []string{}
	for relationRows.Next() {
		var id string
		if err := relationRows.Scan(&id); err != nil {
			relationRows.Close()
			return err
		}
		relationIDs = append(relationIDs, id)
	}
	if err := relationRows.Err(); err != nil {
		relationRows.Close()
		return err
	}
	relationRows.Close()

	if _, err = tx.Exec(ctx, `UPDATE source_entities SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2`, organizationID, sourceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE source_relationships SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2`, organizationID, sourceID); err != nil {
		return err
	}
	for _, id := range entityIDs {
		if _, err := recomputeEntityFromCurrentObservations(ctx, tx, organizationID, id); err != nil {
			return err
		}
	}
	for _, id := range relationIDs {
		if _, err := recomputeRelationshipFromCurrentObservations(ctx, tx, organizationID, id); err != nil {
			return err
		}
	}
	return nil
}

func environmentForAdapter(organizationID string, value environmentConnection) cloud.Environment {
	return cloud.Environment{ID: value.ID.String(), OrganizationID: organizationID, Provider: valueOrEmpty(value.Provider), ExternalID: valueOrEmpty(value.ExternalID), DisplayName: value.DisplayName, SourceID: valueOrEmpty(value.SourceID), TargetID: valueOrEmpty(value.TargetID), Configuration: value.Configuration}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nextDailyScan(id uuid.UUID, now time.Time) time.Time {
	seed := int(id[0])<<8 | int(id[1])
	jitter := time.Duration(seed%240) * time.Minute
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 1, 0, 0, 0, time.UTC).Add(jitter)
	return next
}

func writeCloudError(w http.ResponseWriter, err error) {
	code, message, retryable, retryAfter := cloud.SafeError(err)
	status := http.StatusBadGateway
	if code == "authentication_failed" || code == "permission_denied" {
		status = http.StatusUnauthorized
	}
	if code == "connector_unavailable" {
		status = http.StatusServiceUnavailable
	}
	if retryAfter > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())))
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable}})
}

func jsonBytes(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func (s *Server) trackFirstResultViewed(ctx context.Context, principal Principal, surface string) {
	_, _ = s.db(ctx).Exec(ctx, `INSERT INTO product_events(id,organization_id,actor_id,event_type,properties) VALUES($1,$2,$3,'first_result_viewed',$4) ON CONFLICT DO NOTHING`, uuid.New(), principal.OrganizationID, principal.Subject, jsonBytes(map[string]any{"surface": surface}))
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLSTATE 23505")
}
