package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/cloud"
	"github.com/barrikadelabs/barrikade-lens/internal/githubapp"
	"github.com/barrikadelabs/barrikade-lens/internal/identity"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

type Config struct {
	Pool                       *pgxpool.Pool
	WorkerPool                 *pgxpool.Pool
	JWTSecret                  []byte
	AuthMode                   string
	DevAdminToken              string
	DefaultOrganizationID      string
	DefaultOrganizationName    string
	PublicURL                  string
	Issuer                     string
	Logger                     *slog.Logger
	UIDir                      string
	OIDCIssuer                 string
	OIDCClientID               string
	OIDCClientSecret           string
	OIDCRedirectURI            string
	OIDCAdminGroup             string
	ClerkIssuer                string
	ClerkPublishableKey        string
	ClerkAuthorizedParty       string
	ClerkWebhookSecret         string
	ClerkSecretKey             string
	ClerkAPIBaseURL            string
	GitHubWebhookSecret        []byte
	GitHubClient               *githubapp.Client
	GitHubAppSlug              string
	ExposureEnabled            bool
	SelfServeEnabled           bool
	AWSConnectorEnabled        bool
	AzureConnectorEnabled      bool
	GCPConnectorEnabled        bool
	EndpointConnectorEnabled   bool
	KubernetesConnectorEnabled bool
	GitHubConnectorEnabled     bool
	CISOOverviewV2Enabled      bool
	EndpointHandoffEnabled     bool
	CloudAdapters              cloud.Registry
	AWSBrokerRoleARN           string
	AzureApplicationID         string
	GCPWorkloadIssuer          string
	GCPWorkloadAudience        string
	GCPAssertionAudience       string
	ManagedIdentityPrincipalID string
}

type Server struct {
	config       Config
	auth         *Authenticator
	mux          *http.ServeMux
	oidcProvider *oidc.Provider
	oidcVerifier *oidc.IDTokenVerifier
	oauthConfig  oauth2.Config
}

func NewServer(ctx context.Context, config Config) (*Server, error) {
	if config.Pool == nil {
		return nil, fmt.Errorf("database pool is required")
	}
	if config.WorkerPool == nil {
		config.WorkerPool = config.Pool
	}
	if len(config.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT secret must contain at least 32 bytes")
	}
	if config.DefaultOrganizationID == "" {
		config.DefaultOrganizationID = "default"
	}
	if config.DefaultOrganizationName == "" {
		config.DefaultOrganizationName = "Lens Organization"
	}
	if config.Issuer == "" {
		config.Issuer = "barrikade-lens-hub"
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.AuthMode == "" {
		config.AuthMode = "development"
	}
	if config.AuthMode != "clerk" && config.AuthMode != "oidc" && config.AuthMode != "development" {
		return nil, fmt.Errorf("auth mode must be clerk, oidc, or development")
	}
	if config.AuthMode == "clerk" {
		if config.ClerkIssuer == "" || config.ClerkPublishableKey == "" || config.ClerkAuthorizedParty == "" || config.ClerkSecretKey == "" || config.ClerkWebhookSecret == "" {
			return nil, fmt.Errorf("Clerk issuer, publishable key, authorized party, secret key, and webhook secret are required in Clerk auth mode")
		}
		if config.DevAdminToken != "" {
			return nil, fmt.Errorf("development bootstrap token must be unset in Clerk auth mode")
		}
	} else if _, err := config.WorkerPool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,$2) ON CONFLICT(id) DO NOTHING`, config.DefaultOrganizationID, config.DefaultOrganizationName); err != nil {
		return nil, err
	}
	if config.ClerkAPIBaseURL == "" {
		config.ClerkAPIBaseURL = "https://api.clerk.com"
	}
	server := &Server{config: config, mux: http.NewServeMux()}
	server.auth = &Authenticator{Pool: config.WorkerPool, JWTSecret: config.JWTSecret, DevAdminToken: config.DevAdminToken, DefaultOrganizationID: config.DefaultOrganizationID, Issuer: config.Issuer}
	if config.AuthMode == "clerk" {
		provider, err := oidc.NewProvider(ctx, config.ClerkIssuer)
		if err != nil {
			return nil, fmt.Errorf("discover Clerk issuer: %w", err)
		}
		server.auth.ClerkVerifier = provider.Verifier(&oidc.Config{SkipClientIDCheck: true})
		server.auth.ClerkAuthorizedParty = config.ClerkAuthorizedParty
	}
	if config.AuthMode == "oidc" {
		if config.OIDCClientID == "" || config.OIDCRedirectURI == "" {
			return nil, fmt.Errorf("OIDC client ID and redirect URI are required when an issuer is configured")
		}
		provider, err := oidc.NewProvider(ctx, config.OIDCIssuer)
		if err != nil {
			return nil, fmt.Errorf("discover OIDC provider: %w", err)
		}
		server.oidcProvider = provider
		server.oidcVerifier = provider.Verifier(&oidc.Config{ClientID: config.OIDCClientID})
		server.oauthConfig = oauth2.Config{ClientID: config.OIDCClientID, ClientSecret: config.OIDCClientSecret, Endpoint: provider.Endpoint(), RedirectURL: config.OIDCRedirectURI, Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"}}
	}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return requestLog(s.config.Logger, securityHeaders(s.mux)) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.config.Pool.Ping(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "The database is unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	s.mux.HandleFunc("POST /v1/enrollment/exchange", s.rateLimit("enrollment_exchange", 60, 5*time.Minute, remoteRequestKey, s.exchangeEnrollment))
	s.mux.HandleFunc("POST /v1/public/endpoint-handoffs/resolve", s.rateLimit("handoff_resolve", 60, 5*time.Minute, remoteRequestKey, s.resolveEndpointHandoff))
	s.mux.HandleFunc("POST /v1/collector/token", s.rotateCollectorToken)
	s.mux.HandleFunc("GET /v1/auth/config", s.oidcConfig)
	s.mux.HandleFunc("POST /v1/auth/exchange", s.oidcExchange)
	if s.config.ClerkWebhookSecret != "" {
		s.mux.HandleFunc("POST /v1/auth/clerk/webhook", s.clerkWebhook)
	}
	if len(s.config.GitHubWebhookSecret) > 0 {
		s.mux.HandleFunc("POST /v1/connectors/github/webhook", s.githubWebhook)
	}
	if s.config.GitHubClient != nil && s.config.GitHubConnectorEnabled {
		s.mux.HandleFunc("GET /v1/connectors/github/setup", s.githubSetupCallback)
	}
	authenticated := http.NewServeMux()
	authenticated.HandleFunc("GET /v1/session", s.getSession)
	authenticated.HandleFunc("DELETE /v1/account", s.deleteAccount)
	authenticated.HandleFunc("POST /v1/workspaces/bootstrap", s.rateLimit("workspace_bootstrap", 10, 5*time.Minute, principalRequestKey, s.bootstrapWorkspace))
	authenticated.HandleFunc("DELETE /v1/workspaces/current", s.deleteWorkspace)
	authenticated.HandleFunc("GET /v1/environments", s.listEnvironments)
	authenticated.HandleFunc("GET /v1/environments/{id}", s.getEnvironment)
	authenticated.HandleFunc("GET /v1/environments/{id}/activation", s.getEnvironmentActivation)
	authenticated.HandleFunc("POST /v1/environments/setup-sessions", s.rateLimit("setup_creation", 30, 5*time.Minute, principalRequestKey, s.createEnvironmentSetupSession))
	authenticated.HandleFunc("POST /v1/environments/{id}/enrollment-credentials", s.rotateEndpointEnrollmentCredential)
	authenticated.HandleFunc("POST /v1/environments/{id}/handoffs", s.rateLimit("handoff_creation", 30, 5*time.Minute, principalRequestKey, s.createEndpointHandoff))
	authenticated.HandleFunc("DELETE /v1/environments/{id}/handoffs/{handoffId}", s.revokeEndpointHandoff)
	authenticated.HandleFunc("POST /v1/environments/{id}/verify", s.verifyEnvironment)
	authenticated.HandleFunc("PATCH /v1/environments/{id}", s.updateEnvironment)
	authenticated.HandleFunc("POST /v1/environments/{id}/scans", s.createEnvironmentScan)
	authenticated.HandleFunc("GET /v1/environments/{id}/scans/{scanId}", s.getEnvironmentScan)
	authenticated.HandleFunc("DELETE /v1/environments/{id}", s.disconnectEnvironment)
	authenticated.HandleFunc("POST /v1/admin/enrollment-codes", s.createEnrollmentCode)
	authenticated.HandleFunc("POST /v1/admin/service-accounts", s.createServiceAccount)
	authenticated.HandleFunc("DELETE /v1/admin/service-accounts/{id}", s.revokeServiceAccount)
	authenticated.HandleFunc("DELETE /v1/admin/sources/{id}", s.revokeSource)
	authenticated.HandleFunc("POST /v1/discovery/snapshots", s.rateLimit("snapshot_submission", 120, time.Minute, principalRequestKey, s.submitSnapshot))
	authenticated.HandleFunc("GET /v1/discovery/jobs/{id}", s.getJob)
	authenticated.HandleFunc("GET /v1/entities", s.listEntities)
	authenticated.HandleFunc("GET /v1/entities/{id}", s.getEntity)
	authenticated.HandleFunc("GET /v1/overview", s.overview)
	authenticated.HandleFunc("GET /v1/systems", s.listSystems)
	authenticated.HandleFunc("GET /v1/systems/{id}", s.getSystem)
	authenticated.HandleFunc("GET /v1/targets", s.listTargets)
	authenticated.HandleFunc("GET /v1/targets/{id}", s.getTarget)
	authenticated.HandleFunc("PUT /v1/admin/coverage/baselines", s.putCoverageBaselines)
	authenticated.HandleFunc("GET /v1/relationships", s.listRelationships)
	authenticated.HandleFunc("GET /v1/changes", s.listChanges)
	authenticated.HandleFunc("GET /v1/coverage", s.coverage)
	authenticated.HandleFunc("GET /v1/exports", s.exports)
	authenticated.HandleFunc("GET /v1/notifications", s.listNotifications)
	authenticated.HandleFunc("PATCH /v1/notifications/{id}", s.readNotification)
	authenticated.HandleFunc("POST /v1/webhooks", s.createWebhook)
	if s.config.ExposureEnabled {
		authenticated.HandleFunc("GET /v1/exposures", s.listExposures)
		authenticated.HandleFunc("GET /v1/exposures/{id}", s.getExposure)
		authenticated.HandleFunc("GET /v1/systems/{id}/exposure-map", s.getExposureMap)
		authenticated.HandleFunc("GET /v1/entities/{id}/context", s.getEntityContext)
		authenticated.HandleFunc("PUT /v1/entities/{id}/context", s.putEntityContext)
		authenticated.HandleFunc("GET /v1/catalog/search", s.searchCatalog)
		authenticated.HandleFunc("PUT /v1/entities/{id}/catalog-link", s.putCatalogLink)
		authenticated.HandleFunc("DELETE /v1/entities/{id}/catalog-link", s.deleteCatalogLink)
	}
	s.mux.Handle("/v1/", s.auth.Middleware(s.tenantTransaction(authenticated)))
	if s.config.UIDir != "" {
		s.mux.Handle("/", spaFileHandler(s.config.UIDir))
	}
}

func spaFileHandler(directory string) http.Handler {
	files := http.FileServer(http.Dir(directory))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relative := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), string(filepath.Separator))
		candidate := filepath.Join(directory, relative)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(directory, "index.html"))
	})
}

func (s *Server) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "admin:service_accounts")
	if err != nil || !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	var request struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_name", "Service account name is required and must be at most 128 characters")
		return
	}
	if len(request.Scopes) == 0 {
		request.Scopes = []string{"inventory:read"}
	}
	if len(request.Scopes) != 1 || request.Scopes[0] != "inventory:read" {
		writeError(w, http.StatusBadRequest, "invalid_scope", "Registry integration service accounts may only receive inventory:read")
		return
	}
	raw, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not create the service account")
		return
	}
	id := uuid.New()
	_, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO service_accounts(id,organization_id,name,token_hash,scopes) VALUES($1,$2,$3,$4,$5)`, id, principal.OrganizationID, request.Name, tokenHash(raw), request.Scopes)
	if err != nil {
		writeError(w, http.StatusConflict, "service_account_conflict", "A service account with this name already exists")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id.String(), "name": request.Name, "scopes": request.Scopes, "token": raw, "token_displayed_once": true})
}

func (s *Server) revokeServiceAccount(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "admin:service_accounts")
	if err != nil || !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Service account ID is invalid")
		return
	}
	result, err := s.db(r.Context()).Exec(r.Context(), `UPDATE service_accounts SET revoked_at=now() WHERE organization_id=$1 AND id=$2 AND revoked_at IS NULL`, principal.OrganizationID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not revoke the service account")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Active service account not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createEnrollmentCode(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "admin:enrollment")
	if err != nil || !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	var request struct {
		ExpiresInSeconds int    `json:"expires_in_seconds"`
		Uses             int    `json:"uses"`
		SourceType       string `json:"source_type"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if request.ExpiresInSeconds == 0 {
		request.ExpiresInSeconds = 600
	}
	if request.ExpiresInSeconds < 60 || request.ExpiresInSeconds > 86400 {
		writeError(w, http.StatusBadRequest, "invalid_expiry", "Expiry must be between 60 seconds and 24 hours")
		return
	}
	if request.Uses == 0 {
		request.Uses = 1
	}
	if request.Uses < 1 || request.Uses > 10000 {
		writeError(w, http.StatusBadRequest, "invalid_uses", "Uses must be between 1 and 10000")
		return
	}
	if request.SourceType == "" {
		request.SourceType = "endpoint"
	}
	if request.SourceType != "endpoint" && request.SourceType != "repository" && request.SourceType != "kubernetes" {
		writeError(w, 400, "invalid_source_type", "Source type must be endpoint, repository, or kubernetes")
		return
	}
	code, err := enrollmentCode()
	if err != nil {
		writeError(w, 500, "internal_error", "Could not create enrollment code")
		return
	}
	expires := time.Now().UTC().Add(time.Duration(request.ExpiresInSeconds) * time.Second)
	if _, err := s.db(r.Context()).Exec(r.Context(), `INSERT INTO enrollment_codes(code_hash,organization_id,expires_at,uses_remaining,source_type) VALUES($1,$2,$3,$4,$5)`, tokenHash(normalizeCode(code)), principal.OrganizationID, expires, request.Uses, request.SourceType); err != nil {
		writeError(w, 500, "database_error", "Could not save enrollment code")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expires.Format(time.RFC3339), "uses": request.Uses, "source_type": request.SourceType, "hub_url": s.config.PublicURL, "collector_version": Version})
}

func (s *Server) exchangeEnrollment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code              string `json:"code"`
		Hostname          string `json:"hostname"`
		Platform          string `json:"platform"`
		Architecture      string `json:"architecture"`
		CollectorVersion  string `json:"collector_version"`
		IdentityPublicKey string `json:"identity_public_key"`
		IdentityProof     string `json:"identity_proof"`
		SourceType        string `json:"source_type"`
		TargetIdentity    string `json:"target_identity"`
		DisplayName       string `json:"display_name"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if request.Code == "" || request.IdentityPublicKey == "" || request.IdentityProof == "" {
		writeError(w, 400, "invalid_enrollment", "Code and persistent collector identity proof are required; upgrade the Lens collector if identity fields are unavailable")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not start enrollment")
		return
	}
	defer tx.Rollback(r.Context())
	var orgID, sourceType string
	var enrollmentEnvironmentID *uuid.UUID
	var expires time.Time
	var uses int
	err = tx.QueryRow(r.Context(), `SELECT organization_id,expires_at,uses_remaining,source_type,environment_id FROM enrollment_codes WHERE code_hash=$1 AND revoked_at IS NULL FOR UPDATE`, tokenHash(normalizeCode(request.Code))).Scan(&orgID, &expires, &uses, &sourceType, &enrollmentEnvironmentID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && time.Now().After(expires) {
		writeError(w, 401, "invalid_enrollment_code", "The enrollment code is invalid or expired")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not validate enrollment code")
		return
	}
	if enrollmentEnvironmentID != nil {
		var active bool
		if err = tx.QueryRow(r.Context(), `SELECT connection_status='setup_pending' FROM environment_connections WHERE organization_id=$1 AND id=$2`, orgID, *enrollmentEnvironmentID).Scan(&active); err != nil || !active {
			writeError(w, 401, "invalid_enrollment_code", "The enrollment setup is no longer active")
			return
		}
	}
	if request.SourceType != "" && request.SourceType != sourceType {
		writeError(w, 401, "source_type_mismatch", "The enrollment code was issued for a different collector type")
		return
	}
	targetIdentity, displayName := strings.TrimSpace(request.TargetIdentity), strings.TrimSpace(request.DisplayName)
	targetKind := discovery.KindEndpoint
	identityLabel := request.Hostname
	switch sourceType {
	case "endpoint":
		if request.Hostname == "" {
			writeError(w, 400, "invalid_enrollment", "Endpoint hostname is required")
			return
		}
		targetIdentity, displayName = request.Hostname, request.Hostname
	case "kubernetes":
		targetKind = discovery.KindCluster
		identityLabel = targetIdentity
		if targetIdentity == "" {
			writeError(w, 400, "invalid_enrollment", "A persistent Kubernetes cluster identity is required")
			return
		}
		if displayName == "" {
			displayName = targetIdentity
		}
	case "repository":
		targetKind = discovery.KindRepository
		targetIdentity = strings.ToLower(targetIdentity)
		identityLabel = targetIdentity
		if !repositoryPattern.MatchString(targetIdentity) {
			writeError(w, 400, "invalid_enrollment", "Repository identity must be owner/name")
			return
		}
		if displayName == "" {
			displayName = targetIdentity
		}
	default:
		writeError(w, 400, "invalid_source_type", "Enrollment code has an unsupported collector type")
		return
	}
	if err := identity.Verify(request.IdentityPublicKey, request.IdentityProof, request.Code, identityLabel, request.Platform, request.Architecture, request.CollectorVersion); err != nil {
		writeError(w, 401, "invalid_collector_identity", "The collector identity proof is invalid")
		return
	}
	fingerprint, err := identity.Fingerprint(request.IdentityPublicKey)
	if err != nil {
		writeError(w, 400, "invalid_collector_identity", "The collector identity is invalid")
		return
	}
	targetKey := sourceType + ":" + targetIdentity
	if sourceType == "endpoint" {
		targetKey = "installation-key:" + fingerprint
	}
	targetID := discovery.StableID(orgID, targetKind, targetKey)
	publicKey, _ := base64.RawURLEncoding.DecodeString(request.IdentityPublicKey)
	err = tx.QueryRow(r.Context(), `INSERT INTO discovery_targets(organization_id,id,target_type,identity_fingerprint,identity_public_key,identity_quality,name,platform,architecture)
		VALUES($1,$2,$3,$4,$5,'persistent',$6,$7,$8)
		ON CONFLICT(organization_id,identity_fingerprint) WHERE identity_fingerprint IS NOT NULL
		DO UPDATE SET name=EXCLUDED.name,platform=EXCLUDED.platform,architecture=EXCLUDED.architecture,current=true
		RETURNING id`, orgID, targetID, sourceType, fingerprint, publicKey, displayName, request.Platform, request.Architecture).Scan(&targetID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not create discovery target")
		return
	}
	var sourceID string
	var lastSequence uint64
	err = tx.QueryRow(r.Context(), `SELECT id,last_sequence FROM sources WHERE organization_id=$1 AND target_id=$2 AND source_type=$3 AND revoked_at IS NULL FOR UPDATE`, orgID, targetID, sourceType).Scan(&sourceID, &lastSequence)
	if errors.Is(err, pgx.ErrNoRows) {
		sourceUUID, idErr := uuid.NewV7()
		if idErr != nil {
			sourceUUID = uuid.New()
		}
		sourceID = "source:" + sourceUUID.String()
		_, err = tx.Exec(r.Context(), `INSERT INTO sources(organization_id,id,target_id,source_type,name,platform,architecture,collector_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, orgID, sourceID, targetID, sourceType, displayName, request.Platform, request.Architecture, request.CollectorVersion)
	} else if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE sources SET name=$4,platform=$5,architecture=$6,collector_version=$7 WHERE organization_id=$1 AND id=$2 AND target_id=$3`, orgID, sourceID, targetID, displayName, request.Platform, request.Architecture, request.CollectorVersion)
		if err == nil {
			_, err = tx.Exec(r.Context(), `DELETE FROM collector_refresh_tokens WHERE organization_id=$1 AND source_id=$2`, orgID, sourceID)
		}
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not create or rotate discovery source")
		return
	}
	setupKind := map[string]string{"endpoint": "endpoint", "repository": "github_repository", "kubernetes": "kubernetes_cluster"}[sourceType]
	var setupEnvironmentID uuid.UUID
	setupErr := tx.QueryRow(r.Context(), `SELECT environment_id FROM connector_setup_sessions WHERE organization_id=$1 AND token_hash=$2 AND kind=$3 AND state='pending' AND expires_at>now() FOR UPDATE`, orgID, tokenHash(normalizeCode(request.Code)), setupKind).Scan(&setupEnvironmentID)
	if setupErr == nil {
		_, err = tx.Exec(r.Context(), `UPDATE environment_connections SET external_id=COALESCE(NULLIF(external_id,''),$3),display_name=COALESCE(NULLIF(display_name,''),$4),connection_status='connected',target_id=$5,source_id=$6,verified_at=now(),last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE organization_id=$1 AND id=$2`, orgID, setupEnvironmentID, targetIdentity, displayName, targetID, sourceID)
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE connector_setup_sessions SET state='consumed',consumed_at=now() WHERE organization_id=$1 AND environment_id=$2 AND state='pending'`, orgID, setupEnvironmentID)
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE endpoint_setup_handoffs SET revoked_at=now() WHERE organization_id=$1 AND environment_id=$2 AND revoked_at IS NULL`, orgID, setupEnvironmentID)
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,'collector:enrollment','environment.verified','environment',$3,$4)`, uuid.New(), orgID, setupEnvironmentID.String(), jsonBytes(map[string]any{"provider": sourceType}))
		}
	} else if !errors.Is(setupErr, pgx.ErrNoRows) {
		err = setupErr
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not activate the environment")
		return
	}
	if uses <= 1 {
		_, err = tx.Exec(r.Context(), `DELETE FROM enrollment_codes WHERE code_hash=$1`, tokenHash(normalizeCode(request.Code)))
	} else {
		_, err = tx.Exec(r.Context(), `UPDATE enrollment_codes SET uses_remaining=uses_remaining-1 WHERE code_hash=$1`, tokenHash(normalizeCode(request.Code)))
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not consume enrollment code")
		return
	}
	scopes := []string{"discovery:write", "jobs:read"}
	refresh, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not issue credentials")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO collector_refresh_tokens(token_hash,organization_id,source_id,scopes,expires_at) VALUES($1,$2,$3,$4,$5)`, tokenHash(refresh), orgID, sourceID, scopes, time.Now().UTC().Add(90*24*time.Hour))
	if err != nil {
		writeError(w, 500, "database_error", "Could not issue credentials")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not complete enrollment")
		return
	}
	access, accessExpiry, err := s.auth.issueAccessToken(orgID, sourceID, scopes)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not sign access token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hub_url": s.config.PublicURL, "organization_id": orgID, "source_id": sourceID, "target_id": targetID, "sequence": lastSequence, "access_token": access, "access_token_expires_at": accessExpiry.Format(time.RFC3339), "refresh_token": refresh})
}

func (s *Server) rotateCollectorToken(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if request.RefreshToken == "" {
		writeError(w, 400, "invalid_request", "Refresh token is required")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not rotate token")
		return
	}
	defer tx.Rollback(r.Context())
	var orgID, sourceID string
	var scopes []string
	var expires time.Time
	err = tx.QueryRow(r.Context(), `DELETE FROM collector_refresh_tokens WHERE token_hash=$1 RETURNING organization_id,source_id,scopes,expires_at`, tokenHash(request.RefreshToken)).Scan(&orgID, &sourceID, &scopes, &expires)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && time.Now().After(expires) {
		writeError(w, 401, "invalid_refresh_token", "Refresh token is invalid or expired")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not rotate token")
		return
	}
	refresh, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not rotate token")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO collector_refresh_tokens(token_hash,organization_id,source_id,scopes,expires_at) VALUES($1,$2,$3,$4,$5)`, tokenHash(refresh), orgID, sourceID, scopes, time.Now().UTC().Add(90*24*time.Hour))
	if err != nil {
		writeError(w, 500, "database_error", "Could not rotate token")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not rotate token")
		return
	}
	access, accessExpiry, err := s.auth.issueAccessToken(orgID, sourceID, scopes)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not sign access token")
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": access, "access_token_expires_at": accessExpiry.Format(time.RFC3339), "refresh_token": refresh})
}

func (s *Server) submitSnapshot(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "discovery:write")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	var snapshot discovery.Snapshot
	if err := decodeJSON(w, r, &snapshot, 32<<20); err != nil {
		return
	}
	if !discovery.IsSupportedSchemaVersion(snapshot.SchemaVersion) {
		writeError(w, http.StatusUpgradeRequired, "collector_upgrade_required", "This Lens Hub accepts DiscoverySnapshot schemas "+discovery.LegacySchemaVersion+" and "+discovery.SchemaVersion)
		return
	}
	if err := snapshot.Validate(); err != nil {
		writeError(w, 422, "invalid_snapshot", err.Error())
		return
	}
	if snapshot.OrganizationID != principal.OrganizationID || !principal.Admin && snapshot.SourceID != principal.SourceID {
		writeError(w, 403, "source_mismatch", "Snapshot organization and source must match the credential")
		return
	}
	var revokedAt *time.Time
	var expectedSourceType, expectedTargetID string
	if err := s.db(r.Context()).QueryRow(r.Context(), `SELECT source_type,target_id,revoked_at FROM sources WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, snapshot.SourceID).Scan(&expectedSourceType, &expectedTargetID, &revokedAt); err != nil || revokedAt != nil {
		writeError(w, 403, "source_revoked", "The discovery source is unknown or revoked")
		return
	}
	if expectedSourceType != string(snapshot.SourceType) {
		writeError(w, 403, "source_type_mismatch", "Snapshot source type does not match the enrolled source")
		return
	}
	if expectedTargetID != snapshot.TargetID {
		writeError(w, 403, "target_mismatch", "Snapshot target must match the enrolled discovery target")
		return
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not encode snapshot")
		return
	}
	// Serialize submissions per source across Hub replicas. This keeps only one
	// normalization job active while preserving idempotent snapshot retries.
	if _, err = s.db(r.Context()).Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, snapshot.OrganizationID+":"+snapshot.SourceID); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Could not reserve the discovery source")
		return
	}
	var existingID uuid.UUID
	var existingStatus string
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT id,status FROM ingestion_jobs WHERE organization_id=$1 AND snapshot_id=$2`, snapshot.OrganizationID, snapshot.SnapshotID).Scan(&existingID, &existingStatus)
	if err == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"id": existingID, "status": existingStatus})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Could not inspect the discovery queue")
		return
	}
	var active bool
	if err = s.db(r.Context()).QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM ingestion_jobs WHERE organization_id=$1 AND source_id=$2 AND status IN ('pending','processing'))`, snapshot.OrganizationID, snapshot.SourceID).Scan(&active); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Could not inspect the discovery queue")
		return
	}
	if active {
		w.Header().Set("Retry-After", "15")
		writeError(w, http.StatusTooManyRequests, "source_busy", "This source already has a snapshot being processed")
		return
	}
	jobID := uuid.New()
	var id uuid.UUID
	var status string
	err = s.db(r.Context()).QueryRow(r.Context(), `INSERT INTO ingestion_jobs(id,organization_id,source_id,snapshot_id,status,payload) VALUES($1,$2,$3,$4,'pending',$5) ON CONFLICT(organization_id,snapshot_id) DO UPDATE SET snapshot_id=EXCLUDED.snapshot_id RETURNING id,status`, jobID, snapshot.OrganizationID, snapshot.SourceID, snapshot.SnapshotID, payload).Scan(&id, &status)
	if err != nil {
		writeError(w, 500, "database_error", "Could not queue snapshot")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": status})
}

func (s *Server) revokeSource(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "admin:enrollment")
	if err != nil || !principal.Admin {
		writeError(w, 403, "forbidden", "Administrator access is required")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not revoke source")
		return
	}
	defer tx.Rollback(r.Context())
	entityRows, err := tx.Query(r.Context(), `SELECT entity_id FROM source_entities WHERE organization_id=$1 AND source_id=$2 AND current=true`, principal.OrganizationID, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "Could not load source observations")
		return
	}
	entityIDs := []string{}
	for entityRows.Next() {
		var id string
		if entityRows.Scan(&id) == nil {
			entityIDs = append(entityIDs, id)
		}
	}
	entityRows.Close()
	relationRows, err := tx.Query(r.Context(), `SELECT sr.relationship_id,r.from_entity,r.to_entity FROM source_relationships sr JOIN relationships r ON r.organization_id=sr.organization_id AND r.id=sr.relationship_id WHERE sr.organization_id=$1 AND sr.source_id=$2 AND sr.current=true`, principal.OrganizationID, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "Could not load source relationships")
		return
	}
	type affectedRelationship struct{ ID, From, To string }
	relationIDs := []affectedRelationship{}
	for relationRows.Next() {
		var item affectedRelationship
		if relationRows.Scan(&item.ID, &item.From, &item.To) == nil {
			relationIDs = append(relationIDs, item)
		}
	}
	relationRows.Close()
	tag, err := tx.Exec(r.Context(), `UPDATE sources SET revoked_at=now() WHERE organization_id=$1 AND id=$2 AND revoked_at IS NULL`, principal.OrganizationID, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "Could not revoke source")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "Source not found")
		return
	}
	_, _ = tx.Exec(r.Context(), `DELETE FROM collector_refresh_tokens WHERE organization_id=$1 AND source_id=$2`, principal.OrganizationID, r.PathValue("id"))
	_, _ = tx.Exec(r.Context(), `UPDATE source_entities SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2`, principal.OrganizationID, r.PathValue("id"))
	_, _ = tx.Exec(r.Context(), `UPDATE source_relationships SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2`, principal.OrganizationID, r.PathValue("id"))
	for _, entityID := range entityIDs {
		if _, err := recomputeEntityFromCurrentObservations(r.Context(), tx, principal.OrganizationID, entityID); err != nil {
			writeError(w, 500, "database_error", "Could not reconcile source entities")
			return
		}
	}
	for _, relationship := range relationIDs {
		if _, err := recomputeRelationshipFromCurrentObservations(r.Context(), tx, principal.OrganizationID, relationship.ID); err != nil {
			writeError(w, 500, "database_error", "Could not reconcile source relationships")
			return
		}
		for _, entityID := range []string{relationship.From, relationship.To} {
			if err := refreshEntityPosture(r.Context(), tx, principal.OrganizationID, entityID); err != nil {
				writeError(w, 500, "database_error", "Could not reconcile entity posture")
				return
			}
		}
	}
	_, _ = tx.Exec(r.Context(), `UPDATE discovery_targets t SET current=EXISTS(SELECT 1 FROM sources s WHERE s.organization_id=t.organization_id AND s.target_id=t.id AND s.revoked_at IS NULL) WHERE organization_id=$1 AND id=(SELECT target_id FROM sources WHERE organization_id=$1 AND id=$2)`, principal.OrganizationID, r.PathValue("id"))
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not revoke source")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "jobs:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_job_id", "Job ID must be a UUID")
		return
	}
	var status string
	var errorCode, errorMessage *string
	var created time.Time
	var completed *time.Time
	query := `SELECT status,error_code,error_message,created_at,completed_at FROM ingestion_jobs WHERE organization_id=$1 AND id=$2`
	args := []any{principal.OrganizationID, id}
	if !principal.Admin {
		query += ` AND source_id=$3`
		args = append(args, principal.SourceID)
	}
	err = s.db(r.Context()).QueryRow(r.Context(), query, args...).Scan(&status, &errorCode, &errorMessage, &created, &completed)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Job not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not load job")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": status, "error_code": errorCode, "error_message": errorMessage, "created_at": created, "completed_at": completed})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, 400, "invalid_json", "Request body is not valid for this endpoint")
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid_json", "Request must contain one JSON document")
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func normalizeCode(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
}
func enrollmentCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	data := make([]byte, 10)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	for i := range data {
		data[i] = alphabet[int(data[i])%len(alphabet)]
	}
	return string(data[:5]) + "-" + string(data[5:]), nil
}
func queryLimit(r *http.Request) int {
	value, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if value <= 0 {
		value = 100
	}
	if value > 1000 {
		value = 1000
	}
	return value
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/install" || r.URL.Path == "/v1/public/endpoint-handoffs/resolve" {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}
