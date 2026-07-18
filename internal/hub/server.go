package hub

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/githubapp"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

type Config struct {
	Pool                    *pgxpool.Pool
	JWTSecret               []byte
	DevAdminToken           string
	DefaultOrganizationID   string
	DefaultOrganizationName string
	PublicURL               string
	Issuer                  string
	Logger                  *slog.Logger
	UIDir                   string
	OIDCIssuer              string
	OIDCClientID            string
	OIDCClientSecret        string
	OIDCRedirectURI         string
	OIDCAdminGroup          string
	GitHubWebhookSecret     []byte
	GitHubClient            *githubapp.Client
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
	if _, err := config.Pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,$2) ON CONFLICT(id) DO NOTHING`, config.DefaultOrganizationID, config.DefaultOrganizationName); err != nil {
		return nil, err
	}
	server := &Server{config: config, mux: http.NewServeMux()}
	server.auth = &Authenticator{Pool: config.Pool, JWTSecret: config.JWTSecret, DevAdminToken: config.DevAdminToken, DefaultOrganizationID: config.DefaultOrganizationID, Issuer: config.Issuer}
	if config.OIDCIssuer != "" {
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
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("POST /v1/enrollment/exchange", s.exchangeEnrollment)
	s.mux.HandleFunc("POST /v1/collector/token", s.rotateCollectorToken)
	s.mux.HandleFunc("GET /v1/auth/config", s.oidcConfig)
	s.mux.HandleFunc("POST /v1/auth/exchange", s.oidcExchange)
	if len(s.config.GitHubWebhookSecret) > 0 {
		s.mux.HandleFunc("POST /v1/connectors/github/webhook", s.githubWebhook)
	}
	authenticated := http.NewServeMux()
	authenticated.HandleFunc("POST /v1/admin/enrollment-codes", s.createEnrollmentCode)
	authenticated.HandleFunc("DELETE /v1/admin/sources/{id}", s.revokeSource)
	authenticated.HandleFunc("POST /v1/discovery/snapshots", s.submitSnapshot)
	authenticated.HandleFunc("GET /v1/discovery/jobs/{id}", s.getJob)
	authenticated.HandleFunc("GET /v1/entities", s.listEntities)
	authenticated.HandleFunc("GET /v1/entities/{id}", s.getEntity)
	authenticated.HandleFunc("GET /v1/relationships", s.listRelationships)
	authenticated.HandleFunc("GET /v1/changes", s.listChanges)
	authenticated.HandleFunc("GET /v1/coverage", s.coverage)
	authenticated.HandleFunc("GET /v1/exports", s.exports)
	authenticated.HandleFunc("POST /v1/webhooks", s.createWebhook)
	s.mux.Handle("/v1/", s.auth.Middleware(authenticated))
	if s.config.UIDir != "" {
		s.mux.Handle("/", http.FileServer(http.Dir(s.config.UIDir)))
	}
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
	if _, err := s.config.Pool.Exec(r.Context(), `INSERT INTO enrollment_codes(code_hash,organization_id,expires_at,uses_remaining,source_type) VALUES($1,$2,$3,$4,$5)`, tokenHash(normalizeCode(code)), principal.OrganizationID, expires, request.Uses, request.SourceType); err != nil {
		writeError(w, 500, "database_error", "Could not save enrollment code")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expires.Format(time.RFC3339), "uses": request.Uses, "source_type": request.SourceType})
}

func (s *Server) exchangeEnrollment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code             string `json:"code"`
		Hostname         string `json:"hostname"`
		Platform         string `json:"platform"`
		Architecture     string `json:"architecture"`
		CollectorVersion string `json:"collector_version"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if request.Code == "" || request.Hostname == "" {
		writeError(w, 400, "invalid_enrollment", "Code and hostname are required")
		return
	}
	tx, err := s.config.Pool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "database_error", "Could not start enrollment")
		return
	}
	defer tx.Rollback(r.Context())
	var orgID, sourceType string
	var expires time.Time
	var uses int
	err = tx.QueryRow(r.Context(), `SELECT organization_id,expires_at,uses_remaining,source_type FROM enrollment_codes WHERE code_hash=$1 FOR UPDATE`, tokenHash(normalizeCode(request.Code))).Scan(&orgID, &expires, &uses, &sourceType)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && time.Now().After(expires) {
		writeError(w, 401, "invalid_enrollment_code", "The enrollment code is invalid or expired")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not validate enrollment code")
		return
	}
	sourceUUID, err := uuid.NewV7()
	if err != nil {
		sourceUUID = uuid.New()
	}
	sourceID := "source:" + sourceUUID.String()
	_, err = tx.Exec(r.Context(), `INSERT INTO sources(organization_id,id,source_type,name,platform,collector_version) VALUES($1,$2,$3,$4,$5,$6)`, orgID, sourceID, sourceType, request.Hostname, request.Platform, request.CollectorVersion)
	if err != nil {
		writeError(w, 500, "database_error", "Could not create source")
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
	writeJSON(w, http.StatusOK, map[string]any{"hub_url": s.config.PublicURL, "organization_id": orgID, "source_id": sourceID, "access_token": access, "access_token_expires_at": accessExpiry.Format(time.RFC3339), "refresh_token": refresh})
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
	tx, err := s.config.Pool.BeginTx(r.Context(), pgx.TxOptions{})
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
	if err := snapshot.Validate(); err != nil {
		writeError(w, 422, "invalid_snapshot", err.Error())
		return
	}
	if snapshot.OrganizationID != principal.OrganizationID || !principal.Admin && snapshot.SourceID != principal.SourceID {
		writeError(w, 403, "source_mismatch", "Snapshot organization and source must match the credential")
		return
	}
	var revokedAt *time.Time
	var expectedSourceType string
	if err := s.config.Pool.QueryRow(r.Context(), `SELECT source_type,revoked_at FROM sources WHERE organization_id=$1 AND id=$2`, snapshot.OrganizationID, snapshot.SourceID).Scan(&expectedSourceType, &revokedAt); err != nil || revokedAt != nil {
		writeError(w, 403, "source_revoked", "The discovery source is unknown or revoked")
		return
	}
	if expectedSourceType != string(snapshot.SourceType) {
		writeError(w, 403, "source_type_mismatch", "Snapshot source type does not match the enrolled source")
		return
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not encode snapshot")
		return
	}
	jobID := uuid.New()
	var id uuid.UUID
	var status string
	err = s.config.Pool.QueryRow(r.Context(), `INSERT INTO ingestion_jobs(id,organization_id,source_id,snapshot_id,status,payload) VALUES($1,$2,$3,$4,'pending',$5) ON CONFLICT(organization_id,snapshot_id) DO UPDATE SET snapshot_id=EXCLUDED.snapshot_id RETURNING id,status`, jobID, snapshot.OrganizationID, snapshot.SourceID, snapshot.SnapshotID, payload).Scan(&id, &status)
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
	tx, err := s.config.Pool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "database_error", "Could not revoke source")
		return
	}
	defer tx.Rollback(r.Context())
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
	_, _ = tx.Exec(r.Context(), `UPDATE entities e SET current=EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current),stale=NOT EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current AND NOT se.stale) WHERE organization_id=$1`, principal.OrganizationID)
	_, _ = tx.Exec(r.Context(), `UPDATE relationships rel SET current=EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=rel.organization_id AND sr.relationship_id=rel.id AND sr.current),stale=NOT EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=rel.organization_id AND sr.relationship_id=rel.id AND sr.current AND NOT sr.stale) WHERE organization_id=$1`, principal.OrganizationID)
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
	err = s.config.Pool.QueryRow(r.Context(), query, args...).Scan(&status, &errorCode, &errorMessage, &created, &completed)
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
