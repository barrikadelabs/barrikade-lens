package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/githubapp"
	repositoryscanner "github.com/barrikadelabs/barrikade-lens/internal/scanner/repository"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Server) githubConnectorHealthy() bool {
	return s.config.GitHubConnectorEnabled && s.config.GitHubClient != nil &&
		strings.TrimSpace(s.config.GitHubAppSlug) != "" && len(s.config.GitHubWebhookSecret) > 0
}

func validGitHubScannerPermissions(permissions map[string]string) bool {
	if permissions["contents"] != "read" {
		return false
	}
	for name, level := range permissions {
		if level == "none" {
			continue
		}
		if (name != "contents" && name != "metadata") || level != "read" {
			return false
		}
	}
	return true
}

func (s *Server) githubSetupCallback(w http.ResponseWriter, r *http.Request) {
	if !s.githubConnectorHealthy() {
		writeError(w, http.StatusNotFound, "github_not_configured", "The GitHub connector is not available")
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	installationID, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if state == "" || err != nil || installationID <= 0 {
		writeError(w, 400, "invalid_setup_callback", "GitHub setup state or installation ID is invalid")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not finish GitHub setup")
		return
	}
	defer tx.Rollback(r.Context())
	var setupID, environmentID uuid.UUID
	var organizationID string
	err = tx.QueryRow(r.Context(), `SELECT id,environment_id,organization_id FROM connector_setup_sessions WHERE token_hash=$1 AND kind='github_repository' AND state='pending' AND expires_at>now() FOR UPDATE`, tokenHash(normalizeCode(state))).Scan(&setupID, &environmentID, &organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 410, "setup_expired", "This GitHub setup session is invalid, expired, or already used")
		return
	}
	if err == nil {
		var existingOrganization string
		lookupErr := tx.QueryRow(r.Context(), `SELECT organization_id FROM github_installations WHERE installation_id=$1`, installationID).Scan(&existingOrganization)
		if lookupErr == nil && existingOrganization != organizationID {
			writeError(w, 409, "installation_already_bound", "This GitHub installation is already connected to another workspace")
			return
		}
		if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
			err = lookupErr
		}
	}
	var access githubapp.InstallationAccess
	if err == nil {
		access, err = s.config.GitHubClient.InstallationAccess(r.Context(), installationID)
		if err != nil {
			_ = recordProductEvent(r.Context(), s.db(r.Context()), s.config.ProductAnalytics, ProductEvent{OrganizationID: organizationID, Name: "github_authorization_failed", Properties: map[string]any{"failure": "provider"}, DedupeKey: environmentID.String()})
			writeError(w, 502, "github_verification_failed", "GitHub did not verify this installation")
			return
		}
		if !validGitHubScannerPermissions(access.Permissions) {
			_ = recordProductEvent(r.Context(), s.db(r.Context()), s.config.ProductAnalytics, ProductEvent{OrganizationID: organizationID, Name: "github_authorization_failed", Properties: map[string]any{"failure": "validation"}, DedupeKey: environmentID.String()})
			writeError(w, 409, "github_permissions_too_broad", "This GitHub App installation does not have the required read-only scanner permissions")
			return
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO github_installations(installation_id,organization_id,environment_id,account_login,repository_selection,revoked_at,last_error_code,last_error_message,updated_at)
			VALUES($1,$2,$3,$4,$5,NULL,NULL,NULL,now())
			ON CONFLICT(installation_id) DO UPDATE SET environment_id=EXCLUDED.environment_id,account_login=EXCLUDED.account_login,repository_selection=EXCLUDED.repository_selection,revoked_at=NULL,last_error_code=NULL,last_error_message=NULL,updated_at=now()
			WHERE github_installations.organization_id=EXCLUDED.organization_id`, installationID, organizationID, environmentID, "installation:"+strconv.FormatInt(installationID, 10), access.RepositorySelection)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE environment_connections SET configuration=jsonb_set(configuration,'{installation_id}',to_jsonb($3::bigint),true),connection_status='verifying',last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE organization_id=$1 AND id=$2`, organizationID, environmentID, installationID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE connector_setup_sessions SET state='consumed',consumed_at=now() WHERE id=$1`, setupID)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 500, "database_error", "Could not bind the GitHub installation")
		return
	}
	_ = recordProductEvent(r.Context(), s.db(r.Context()), s.config.ProductAnalytics, ProductEvent{OrganizationID: organizationID, Name: "github_authorization_completed", DedupeKey: environmentID.String()})
	// Reconcile immediately so a webhook delivered before the setup callback is
	// not required for first results.
	if access.Token != "" {
		repositories, listErr := s.config.GitHubClient.Repositories(r.Context(), access.Token)
		if listErr != nil {
			_, _ = s.db(r.Context()).Exec(r.Context(), `UPDATE github_installations SET last_error_code='repository_list_failed',last_error_message='GitHub repository access could not be refreshed',updated_at=now() WHERE installation_id=$1 AND organization_id=$2`, installationID, organizationID)
			_, _ = s.db(r.Context()).Exec(r.Context(), `UPDATE environment_connections SET connection_status='auth_error',last_error_code='repository_list_failed',last_error_message='GitHub repository access could not be refreshed',updated_at=now() WHERE organization_id=$1 AND id=$2`, organizationID, environmentID)
			http.Redirect(w, r, strings.TrimSuffix(s.config.PublicURL, "/")+"/connections/"+environmentID.String()+"?github_setup=failed", http.StatusSeeOther)
			return
		}
		reconcileTx, beginErr := s.begin(r.Context())
		if beginErr != nil {
			writeError(w, 500, "database_error", "GitHub was connected, but initial repository scans could not be queued")
			return
		}
		if _, err = reconcileTx.Exec(r.Context(), `DELETE FROM github_repository_selections WHERE installation_id=$1 AND organization_id=$2`, installationID, organizationID); err != nil {
			reconcileTx.Rollback(r.Context())
			writeError(w, 500, "database_error", "GitHub was connected, but repository selection could not be saved")
			return
		}
		for _, repository := range repositories {
			if _, err = reconcileTx.Exec(r.Context(), `INSERT INTO github_repository_selections(installation_id,organization_id,owner,repository) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, installationID, organizationID, strings.ToLower(repository.Owner), strings.ToLower(repository.Name)); err != nil {
				reconcileTx.Rollback(r.Context())
				writeError(w, 500, "database_error", "GitHub was connected, but repository selection could not be saved")
				return
			}
			if err := enqueueRepositoryScan(r.Context(), reconcileTx, organizationID, installationID, repository.Owner, repository.Name, "resolve:setup:"+setupID.String()); err != nil {
				reconcileTx.Rollback(r.Context())
				writeError(w, 500, "database_error", "GitHub was connected, but initial repository scans could not be queued")
				return
			}
		}
		_, err = reconcileTx.Exec(r.Context(), `UPDATE github_installations SET selected_repository_count=$3,first_scan_started_at=CASE WHEN $3>0 THEN COALESCE(first_scan_started_at,now()) ELSE first_scan_started_at END,last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE installation_id=$1 AND organization_id=$2`, installationID, organizationID, len(repositories))
		if err != nil || reconcileTx.Commit(r.Context()) != nil {
			writeError(w, 500, "database_error", "GitHub was connected, but initial repository scans could not be queued")
			return
		}
		if len(repositories) > 0 {
			_ = recordProductEvent(r.Context(), s.db(r.Context()), s.config.ProductAnalytics, ProductEvent{OrganizationID: organizationID, Name: "github_first_scan_started", DedupeKey: strconv.FormatInt(installationID, 10)})
		}
	}
	http.Redirect(w, r, strings.TrimSuffix(s.config.PublicURL, "/")+"/connections/"+environmentID.String()+"?github_setup=complete", http.StatusSeeOther)
}

func (s *Server) getGitHubEnvironmentStatus(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:read")
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	environmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Environment ID is invalid")
		return
	}
	var connection string
	var installationID *int64
	var environmentErrorCode, environmentErrorMessage *string
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT connection_status,(configuration->>'installation_id')::bigint,last_error_code,last_error_message FROM environment_connections WHERE organization_id=$1 AND id=$2 AND kind='github_repository'`, principal.OrganizationID, environmentID).Scan(&connection, &installationID, &environmentErrorCode, &environmentErrorMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "GitHub environment not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not load GitHub discovery status")
		return
	}
	phase := "authorizing"
	selected, pending, processing, complete, failed, ingesting, assetsFound := 0, 0, 0, 0, 0, 0, 0
	var firstStartedAt, revokedAt *time.Time
	var selection, errorCode, errorMessage *string
	if installationID != nil {
		err = s.db(r.Context()).QueryRow(r.Context(), `SELECT repository_selection,selected_repository_count,revoked_at,last_error_code,last_error_message,first_scan_started_at FROM github_installations WHERE organization_id=$1 AND installation_id=$2 AND environment_id=$3`, principal.OrganizationID, *installationID, environmentID).Scan(&selection, &selected, &revokedAt, &errorCode, &errorMessage, &firstStartedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			installationID = nil
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "database_error", "Could not load GitHub discovery status")
			return
		}
	}
	if installationID != nil {
		_ = s.db(r.Context()).QueryRow(r.Context(), `WITH latest AS (
			SELECT DISTINCT ON (j.owner,j.repository) j.status FROM repository_scan_jobs j
			JOIN github_repository_selections rs ON rs.installation_id=j.installation_id AND rs.organization_id=j.organization_id AND rs.owner=lower(j.owner) AND rs.repository=lower(j.repository)
			WHERE j.organization_id=$1 AND j.installation_id=$2 ORDER BY j.owner,j.repository,j.created_at DESC
		) SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='processing'),count(*) FILTER(WHERE status='complete'),count(*) FILTER(WHERE status='failed') FROM latest`, principal.OrganizationID, *installationID).Scan(&pending, &processing, &complete, &failed)
		_ = s.db(r.Context()).QueryRow(r.Context(), `SELECT count(DISTINCT se.entity_id) FROM github_repositories gr JOIN source_entities se ON se.organization_id=gr.organization_id AND se.source_id=gr.source_id AND se.current=true WHERE gr.organization_id=$1 AND gr.installation_id=$2`, principal.OrganizationID, *installationID).Scan(&assetsFound)
		_ = s.db(r.Context()).QueryRow(r.Context(), `SELECT count(*) FROM github_repositories gr JOIN ingestion_jobs ij ON ij.organization_id=gr.organization_id AND ij.source_id=gr.source_id AND ij.status IN ('pending','processing') WHERE gr.organization_id=$1 AND gr.installation_id=$2`, principal.OrganizationID, *installationID).Scan(&ingesting)
	}
	switch {
	case connection == "disconnected":
		phase = "disconnected"
	case revokedAt != nil:
		phase = "revoked"
	case connection == "auth_error" || errorCode != nil || environmentErrorCode != nil:
		phase = "failed"
	case installationID == nil:
		phase = "authorizing"
	case selected == 0:
		phase = "awaiting_selection"
	case failed > 0 && (assetsFound > 0 || complete > 0 && ingesting == 0):
		phase = "partial"
	case failed >= selected:
		phase = "failed"
	case complete >= selected && ingesting == 0:
		phase = "ready"
	default:
		phase = "scanning"
	}
	if errorCode == nil {
		errorCode, errorMessage = environmentErrorCode, environmentErrorMessage
	}
	if phase == "failed" && errorMessage == nil {
		message := "Some selected repositories could not be scanned. Check GitHub access and retry discovery."
		errorMessage = &message
	}
	done := complete + failed
	progress := 0
	if selected > 0 {
		progress = done * 100 / selected
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"environment_id": environmentID, "phase": phase, "connection_status": connection,
		"repository_selection": selection, "selected_repositories": selected,
		"progress": map[string]int{"percent": progress, "pending": pending, "processing": processing + ingesting, "complete": complete, "failed": failed},
		"summary":  map[string]int{"assets_found": assetsFound}, "first_scan_started_at": firstStartedAt,
		"safe_error": map[string]any{"code": errorCode, "message": errorMessage},
	})
}

func (s *Server) reconcileGitHubEnvironment(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	if !s.githubConnectorHealthy() {
		writeError(w, http.StatusServiceUnavailable, "github_not_configured", "The GitHub connector is not available")
		return
	}
	environmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Environment ID is invalid")
		return
	}
	var installationID int64
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT installation_id FROM github_installations WHERE organization_id=$1 AND environment_id=$2 AND revoked_at IS NULL`, principal.OrganizationID, environmentID).Scan(&installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "github_reauthorization_required", "GitHub access is not active; authorize the App again")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not retry GitHub discovery")
		return
	}
	access, err := s.config.GitHubClient.InstallationAccess(r.Context(), installationID)
	if err != nil || !validGitHubScannerPermissions(access.Permissions) {
		_, _ = s.db(r.Context()).Exec(r.Context(), `UPDATE github_installations SET last_error_code='access_revoked',last_error_message='GitHub access must be authorized again',updated_at=now() WHERE organization_id=$1 AND installation_id=$2`, principal.OrganizationID, installationID)
		writeError(w, http.StatusConflict, "github_reauthorization_required", "GitHub access must be authorized again")
		return
	}
	repositories, err := s.config.GitHubClient.Repositories(r.Context(), access.Token)
	if err != nil {
		writeError(w, http.StatusBadGateway, "github_unavailable", "GitHub repository access could not be refreshed")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not retry GitHub discovery")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `DELETE FROM github_repository_selections WHERE installation_id=$1 AND organization_id=$2`, installationID, principal.OrganizationID); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not retry GitHub discovery")
		return
	}
	for _, repository := range repositories {
		if _, err = tx.Exec(r.Context(), `INSERT INTO github_repository_selections(installation_id,organization_id,owner,repository) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, installationID, principal.OrganizationID, strings.ToLower(repository.Owner), strings.ToLower(repository.Name)); err != nil {
			writeError(w, http.StatusInternalServerError, "database_error", "Could not retry GitHub discovery")
			return
		}
		if err = enqueueRepositoryScan(r.Context(), tx, principal.OrganizationID, installationID, repository.Owner, repository.Name, "resolve:retry:"+uuid.NewString()); err != nil {
			writeError(w, http.StatusInternalServerError, "database_error", "Could not retry GitHub discovery")
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE github_installations SET selected_repository_count=$3,last_error_code=NULL,last_error_message=NULL,revoked_at=NULL,first_scan_started_at=CASE WHEN $3>0 THEN COALESCE(first_scan_started_at,now()) ELSE first_scan_started_at END,updated_at=now() WHERE organization_id=$1 AND installation_id=$2`, principal.OrganizationID, installationID, len(repositories))
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE environment_connections SET connection_status='verifying',last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, environmentID)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not retry GitHub discovery")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"environment_id": environmentID, "selected_repositories": len(repositories), "phase": func() string {
		if len(repositories) == 0 {
			return "awaiting_selection"
		}
		return "scanning"
	}()})
}

func (s *Server) reauthorizeGitHubEnvironment(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	if !s.githubConnectorHealthy() {
		writeError(w, http.StatusServiceUnavailable, "github_not_configured", "The GitHub connector is not available")
		return
	}
	environmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Environment ID is invalid")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not prepare GitHub authorization")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not prepare GitHub authorization")
		return
	}
	defer tx.Rollback(r.Context())
	var displayName string
	err = tx.QueryRow(r.Context(), `UPDATE environment_connections SET connection_status='setup_pending',configuration=configuration-'installation_id',last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE organization_id=$1 AND id=$2 AND kind='github_repository' AND connection_status<>'disconnected' RETURNING display_name`, principal.OrganizationID, environmentID).Scan(&displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "GitHub environment not found")
		return
	}
	setupID, expiresAt := uuid.New(), time.Now().UTC().Add(15*time.Minute)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE connector_setup_sessions SET state='cancelled' WHERE organization_id=$1 AND environment_id=$2 AND state='pending'`, principal.OrganizationID, environmentID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO connector_setup_sessions(id,organization_id,environment_id,token_hash,kind,setup_payload,created_by,expires_at) VALUES($1,$2,$3,$4,'github_repository','{}',$5,$6)`, setupID, principal.OrganizationID, environmentID, tokenHash(normalizeCode(token)), principal.Subject, expiresAt)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not prepare GitHub authorization")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": setupID, "environment_id": environmentID, "kind": "github_repository", "expires_at": expiresAt, "setup": s.environmentSetupInstructions("github_repository", "github", "", displayName, token, map[string]any{}), "token_displayed_once": true})
}

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	delivery, event, signature := r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event"), r.Header.Get("X-Hub-Signature-256")
	if delivery == "" || event == "" || signature == "" {
		writeError(w, 400, "invalid_webhook", "Required GitHub webhook headers are missing")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "invalid_webhook", "Webhook body could not be read")
		return
	}
	mac := hmac.New(sha256.New, s.config.GitHubWebhookSecret)
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		writeError(w, 401, "invalid_signature", "GitHub webhook signature is invalid")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not process webhook")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `INSERT INTO github_webhook_deliveries(delivery_id,event_type) VALUES($1,$2) ON CONFLICT DO NOTHING`, delivery, event)
	if err != nil {
		writeError(w, 500, "database_error", "Could not record webhook")
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, 200, map[string]string{"status": "duplicate"})
		return
	}
	switch event {
	case "installation":
		var payload struct {
			Action       string `json:"action"`
			Installation struct {
				ID      int64 `json:"id"`
				Account struct {
					Login string `json:"login"`
				} `json:"account"`
			} `json:"installation"`
			Repositories []struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Installation.ID == 0 {
			writeError(w, 400, "invalid_webhook", "Installation payload is malformed")
			return
		}
		if payload.Action == "deleted" {
			err = removeInstallationSources(r.Context(), tx, payload.Installation.ID)
			if err == nil {
				_, err = tx.Exec(r.Context(), `DELETE FROM github_repository_selections WHERE installation_id=$1`, payload.Installation.ID)
			}
			if err == nil {
				_, err = tx.Exec(r.Context(), `UPDATE github_installations SET revoked_at=now(),last_error_code='access_revoked',last_error_message='GitHub access was revoked; authorize the App again',updated_at=now() WHERE installation_id=$1`, payload.Installation.ID)
			}
			if err == nil {
				_, err = tx.Exec(r.Context(), `UPDATE environment_connections SET connection_status='auth_error',last_error_code='access_revoked',last_error_message='GitHub access was revoked; authorize the App again',source_id=NULL,target_id=NULL,updated_at=now() WHERE id=(SELECT environment_id FROM github_installations WHERE installation_id=$1)`, payload.Installation.ID)
			}
		} else {
			var organizationID string
			err = tx.QueryRow(r.Context(), `UPDATE github_installations SET account_login=$2 WHERE installation_id=$1 RETURNING organization_id`, payload.Installation.ID, payload.Installation.Account.Login).Scan(&organizationID)
			if errors.Is(err, pgx.ErrNoRows) {
				// The signed setup callback binds the installation to a tenant. An
				// unclaimed webhook is never assigned to a default organization.
				err = nil
			} else if err == nil {
				for _, repository := range payload.Repositories {
					_, err = tx.Exec(r.Context(), `INSERT INTO github_repository_selections(installation_id,organization_id,owner,repository) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, payload.Installation.ID, organizationID, strings.ToLower(repository.Owner.Login), strings.ToLower(repository.Name))
					if err == nil {
						err = enqueueRepositoryScan(r.Context(), tx, organizationID, payload.Installation.ID, repository.Owner.Login, repository.Name, "resolve:"+delivery)
					}
					if err != nil {
						break
					}
				}
			}
		}
	case "installation_repositories":
		var payload struct {
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
			RepositoriesAdded []struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories_added"`
			RepositoriesRemoved []struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories_removed"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Installation.ID == 0 {
			writeError(w, 400, "invalid_webhook", "Installation repositories payload is malformed")
			return
		}
		var orgID string
		if err = tx.QueryRow(r.Context(), `SELECT organization_id FROM github_installations WHERE installation_id=$1`, payload.Installation.ID).Scan(&orgID); err == nil {
			for _, repository := range payload.RepositoriesAdded {
				_, err = tx.Exec(r.Context(), `INSERT INTO github_repository_selections(installation_id,organization_id,owner,repository) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, payload.Installation.ID, orgID, strings.ToLower(repository.Owner.Login), strings.ToLower(repository.Name))
				if err == nil {
					err = enqueueRepositoryScan(r.Context(), tx, orgID, payload.Installation.ID, repository.Owner.Login, repository.Name, "resolve:"+delivery)
				}
				if err != nil {
					break
				}
			}
			for _, repository := range payload.RepositoriesRemoved {
				if err == nil {
					err = removeRepositorySource(r.Context(), tx, payload.Installation.ID, repository.Owner.Login, repository.Name)
				}
			}
			if err == nil {
				_, err = tx.Exec(r.Context(), `UPDATE github_installations SET selected_repository_count=GREATEST(0,selected_repository_count+$2-$3),last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE installation_id=$1`, payload.Installation.ID, len(payload.RepositoriesAdded), len(payload.RepositoriesRemoved))
			}
		}
	case "repository":
		var payload struct {
			Action       string `json:"action"`
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
			Repository struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Installation.ID == 0 || payload.Repository.Name == "" {
			writeError(w, 400, "invalid_webhook", "Repository payload is malformed")
			return
		}
		if payload.Action == "deleted" || payload.Action == "archived" {
			err = removeRepositorySource(r.Context(), tx, payload.Installation.ID, payload.Repository.Owner.Login, payload.Repository.Name)
		} else {
			var orgID string
			if err = tx.QueryRow(r.Context(), `SELECT organization_id FROM github_installations WHERE installation_id=$1`, payload.Installation.ID).Scan(&orgID); err == nil {
				_, err = tx.Exec(r.Context(), `INSERT INTO github_repository_selections(installation_id,organization_id,owner,repository) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, payload.Installation.ID, orgID, strings.ToLower(payload.Repository.Owner.Login), strings.ToLower(payload.Repository.Name))
				if err == nil {
					err = enqueueRepositoryScan(r.Context(), tx, orgID, payload.Installation.ID, payload.Repository.Owner.Login, payload.Repository.Name, "resolve:"+delivery)
				}
			}
		}
	case "push":
		var payload struct {
			After        string `json:"after"`
			Deleted      bool   `json:"deleted"`
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
			Repository struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Installation.ID == 0 || payload.Repository.Name == "" {
			writeError(w, 400, "invalid_webhook", "Push payload is malformed")
			return
		}
		if !payload.Deleted && strings.Trim(payload.After, "0") != "" {
			var orgID string
			if err = tx.QueryRow(r.Context(), `SELECT organization_id FROM github_installations WHERE installation_id=$1`, payload.Installation.ID).Scan(&orgID); err == nil {
				err = enqueueRepositoryScan(r.Context(), tx, orgID, payload.Installation.ID, payload.Repository.Owner.Login, payload.Repository.Name, payload.After)
			}
		}
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not apply webhook")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not commit webhook")
		return
	}
	writeJSON(w, 202, map[string]string{"status": "accepted"})
}

func enqueueRepositoryScan(ctx context.Context, tx pgx.Tx, orgID string, installationID int64, owner, repository, commit string) error {
	if owner == "" || repository == "" || commit == "" {
		return fmt.Errorf("repository scan coordinates are incomplete")
	}
	_, err := tx.Exec(ctx, `INSERT INTO repository_scan_jobs(id,organization_id,installation_id,owner,repository,commit_sha,status) VALUES($1,$2,$3,$4,$5,$6,'pending') ON CONFLICT DO NOTHING`, uuid.New(), orgID, installationID, owner, repository, commit)
	return err
}

type githubRepositorySource struct {
	organizationID string
	owner          string
	repository     string
	sourceID       string
}

func removeInstallationSources(ctx context.Context, tx pgx.Tx, installationID int64) error {
	rows, err := tx.Query(ctx, `SELECT organization_id,owner,repository,source_id FROM github_repositories WHERE installation_id=$1`, installationID)
	if err != nil {
		return err
	}
	items := []githubRepositorySource{}
	for rows.Next() {
		var item githubRepositorySource
		if err := rows.Scan(&item.organizationID, &item.owner, &item.repository, &item.sourceID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := removeGitHubSource(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

func removeRepositorySource(ctx context.Context, tx pgx.Tx, installationID int64, owner, repository string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM github_repository_selections WHERE installation_id=$1 AND owner=$2 AND repository=$3`, installationID, strings.ToLower(owner), strings.ToLower(repository)); err != nil {
		return err
	}
	var item githubRepositorySource
	err := tx.QueryRow(ctx, `SELECT organization_id,owner,repository,source_id FROM github_repositories WHERE installation_id=$1 AND owner=$2 AND repository=$3`, installationID, strings.ToLower(owner), strings.ToLower(repository)).Scan(&item.organizationID, &item.owner, &item.repository, &item.sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return removeGitHubSource(ctx, tx, item)
}

func removeGitHubSource(ctx context.Context, tx pgx.Tx, item githubRepositorySource) error {
	rows, err := tx.Query(ctx, `SELECT entity_id FROM source_entities WHERE organization_id=$1 AND source_id=$2 AND current=true`, item.organizationID, item.sourceID)
	if err != nil {
		return err
	}
	entityIDs := []string{}
	for rows.Next() {
		var entityID string
		if err := rows.Scan(&entityID); err != nil {
			rows.Close()
			return err
		}
		entityIDs = append(entityIDs, entityID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	relationRows, err := tx.Query(ctx, `SELECT sr.relationship_id,r.from_entity,r.to_entity FROM source_relationships sr JOIN relationships r ON r.organization_id=sr.organization_id AND r.id=sr.relationship_id WHERE sr.organization_id=$1 AND sr.source_id=$2 AND sr.current=true`, item.organizationID, item.sourceID)
	if err != nil {
		return err
	}
	type affectedRelationship struct{ ID, From, To string }
	relations := []affectedRelationship{}
	for relationRows.Next() {
		var relation affectedRelationship
		if err := relationRows.Scan(&relation.ID, &relation.From, &relation.To); err != nil {
			relationRows.Close()
			return err
		}
		relations = append(relations, relation)
	}
	if err := relationRows.Err(); err != nil {
		relationRows.Close()
		return err
	}
	relationRows.Close()
	if _, err = tx.Exec(ctx, `UPDATE source_entities SET current=false,stale=true,consecutive_full_misses=3 WHERE organization_id=$1 AND source_id=$2`, item.organizationID, item.sourceID); err != nil {
		return err
	}
	snapshot := discovery.Snapshot{SnapshotID: uuid.NewString(), OrganizationID: item.organizationID, SourceID: item.sourceID}
	for _, entityID := range entityIDs {
		current, err := recomputeEntityFromCurrentObservations(ctx, tx, item.organizationID, entityID)
		if err != nil {
			return err
		}
		if !current {
			if err := recordChange(ctx, tx, snapshot, "entity.removed", entityID, &changeMetadata{Category: "freshness", Summary: "System removed from current inventory"}); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE source_relationships SET current=false,stale=true,consecutive_full_misses=3 WHERE organization_id=$1 AND source_id=$2`, item.organizationID, item.sourceID); err != nil {
		return err
	}
	for _, relation := range relations {
		if _, err := recomputeRelationshipFromCurrentObservations(ctx, tx, item.organizationID, relation.ID); err != nil {
			return err
		}
		for _, entityID := range []string{relation.From, relation.To} {
			if err := refreshEntityPosture(ctx, tx, item.organizationID, entityID); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE sources SET revoked_at=now() WHERE organization_id=$1 AND id=$2`, item.organizationID, item.sourceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE discovery_targets SET current=false WHERE organization_id=$1 AND id=(SELECT target_id FROM sources WHERE organization_id=$1 AND id=$2)`, item.organizationID, item.sourceID); err != nil {
		return err
	}
	if err = enqueueExposureEvaluation(ctx, tx, item.organizationID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM github_repositories WHERE organization_id=$1 AND source_id=$2`, item.organizationID, item.sourceID)
	return err
}

type RepositoryWorker struct {
	Pool         *pgxpool.Pool
	Client       *githubapp.Client
	Logger       *slog.Logger
	PollInterval time.Duration
}

func (w RepositoryWorker) Run(ctx context.Context) error {
	if w.Client == nil {
		return fmt.Errorf("GitHub App client is required")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.PollInterval == 0 {
		w.PollInterval = time.Second
	}
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	reconciliation := time.NewTicker(24 * time.Hour)
	defer reconciliation.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.processOne(ctx); err != nil {
				w.Logger.Warn("repository scan failed", "error", err)
			}
		case <-reconciliation.C:
			if err := w.enqueueReconciliation(ctx); err != nil {
				w.Logger.Warn("nightly GitHub reconciliation failed", "error", err)
			}
		}
	}
}

func (w RepositoryWorker) enqueueReconciliation(ctx context.Context) error {
	rows, err := w.Pool.Query(ctx, `SELECT installation_id,organization_id FROM github_installations WHERE revoked_at IS NULL`)
	if err != nil {
		return err
	}
	type installation struct {
		id  int64
		org string
	}
	items := []installation{}
	for rows.Next() {
		var item installation
		if rows.Scan(&item.id, &item.org) == nil {
			items = append(items, item)
		}
	}
	rows.Close()
	for _, item := range items {
		token, _, err := w.Client.InstallationToken(ctx, item.id)
		if err != nil {
			return err
		}
		repositories, err := w.Client.Repositories(ctx, token)
		if err != nil {
			return err
		}
		if err := w.removeRepositoriesMissingFromReconciliation(ctx, item.id, repositories); err != nil {
			return err
		}
		for _, repository := range repositories {
			commit, err := w.Client.HeadCommit(ctx, token, repository)
			if err != nil {
				w.Logger.Debug("GitHub repository head unavailable", "repository", repository.Owner+"/"+repository.Name, "error", err)
				continue
			}
			_, err = w.Pool.Exec(ctx, `INSERT INTO repository_scan_jobs(id,organization_id,installation_id,owner,repository,commit_sha,status) VALUES($1,$2,$3,$4,$5,$6,'pending') ON CONFLICT DO NOTHING`, uuid.New(), item.org, item.id, repository.Owner, repository.Name, commit)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (w RepositoryWorker) removeRepositoriesMissingFromReconciliation(ctx context.Context, installationID int64, repositories []githubapp.Repository) error {
	present := map[string]struct{}{}
	for _, repository := range repositories {
		present[strings.ToLower(repository.Owner)+"/"+strings.ToLower(repository.Name)] = struct{}{}
	}
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var organizationID string
	if err := tx.QueryRow(ctx, `SELECT organization_id FROM github_installations WHERE installation_id=$1 AND revoked_at IS NULL`, installationID).Scan(&organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM github_repository_selections WHERE installation_id=$1`, installationID); err != nil {
		return err
	}
	for _, repository := range repositories {
		if _, err := tx.Exec(ctx, `INSERT INTO github_repository_selections(installation_id,organization_id,owner,repository) VALUES($1,$2,$3,$4)`, installationID, organizationID, strings.ToLower(repository.Owner), strings.ToLower(repository.Name)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE github_installations SET selected_repository_count=$2,last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE installation_id=$1`, installationID, len(repositories)); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT organization_id,owner,repository,source_id FROM github_repositories WHERE installation_id=$1`, installationID)
	if err != nil {
		return err
	}
	removed := []githubRepositorySource{}
	for rows.Next() {
		var item githubRepositorySource
		if err := rows.Scan(&item.organizationID, &item.owner, &item.repository, &item.sourceID); err != nil {
			rows.Close()
			return err
		}
		if _, exists := present[item.owner+"/"+item.repository]; !exists {
			removed = append(removed, item)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range removed {
		if err := removeGitHubSource(ctx, tx, item); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (w RepositoryWorker) processOne(ctx context.Context) (bool, error) {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	var id uuid.UUID
	var orgID, owner, repository, commit string
	var installationID int64
	var attempts int
	err = tx.QueryRow(ctx, `SELECT id,organization_id,installation_id,owner,repository,commit_sha,attempts FROM repository_scan_jobs WHERE status='pending' AND created_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &orgID, &installationID, &owner, &repository, &commit, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(ctx)
		return false, nil
	}
	if err != nil {
		tx.Rollback(ctx)
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE repository_scan_jobs SET status='processing',attempts=attempts+1 WHERE id=$1`, id); err != nil {
		tx.Rollback(ctx)
		return true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, err
	}
	var selected bool
	if err = w.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM github_repository_selections WHERE installation_id=$1 AND organization_id=$2 AND owner=lower($3) AND repository=lower($4))`, installationID, orgID, owner, repository).Scan(&selected); err != nil {
		return true, err
	}
	if !selected {
		_, err = w.Pool.Exec(ctx, `DELETE FROM repository_scan_jobs WHERE id=$1`, id)
		return true, err
	}
	if strings.HasPrefix(commit, "resolve:") {
		token, _, resolveErr := w.Client.InstallationToken(ctx, installationID)
		if resolveErr == nil {
			commit, resolveErr = w.Client.HeadCommit(ctx, token, githubapp.Repository{Owner: owner, Name: repository, DefaultBranch: "HEAD"})
		}
		if resolveErr != nil {
			jobErr := fmt.Errorf("resolve repository head: %w", resolveErr)
			status := "pending"
			if attempts+1 >= 5 {
				status = "failed"
			}
			_, markErr := w.Pool.Exec(ctx, `UPDATE repository_scan_jobs SET status=$2,error_message=$3,created_at=now()+interval '1 minute',completed_at=CASE WHEN $2='failed' THEN now() ELSE NULL END WHERE id=$1`, id, status, safeError(jobErr))
			if markErr != nil {
				return true, fmt.Errorf("resolve: %v; mark: %w", jobErr, markErr)
			}
			return true, jobErr
		}
	}
	jobErr := w.scan(ctx, orgID, installationID, owner, repository, commit)
	if jobErr != nil {
		status := "pending"
		if attempts+1 >= 5 {
			status = "failed"
		}
		_, markErr := w.Pool.Exec(ctx, `UPDATE repository_scan_jobs SET status=$2,error_message=$3,created_at=now()+interval '1 minute',completed_at=CASE WHEN $2='failed' THEN now() ELSE NULL END WHERE id=$1`, id, status, safeError(jobErr))
		if markErr != nil {
			return true, fmt.Errorf("scan: %v; mark: %w", jobErr, markErr)
		}
		return true, jobErr
	}
	_, err = w.Pool.Exec(ctx, `UPDATE repository_scan_jobs SET status='complete',error_message=NULL,completed_at=now() WHERE id=$1`, id)
	return true, err
}
func (w RepositoryWorker) scan(ctx context.Context, orgID string, installationID int64, owner, repository, commit string) error {
	token, _, err := w.Client.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "lens-repository-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := w.Client.DownloadRepository(ctx, token, owner, repository, commit, temporary); err != nil {
		return err
	}
	repositoryURL := "https://github.com/" + owner + "/" + repository
	sourceID := discovery.StableID(orgID, discovery.KindRepository, repositoryURL)
	targetID := sourceID
	_, err = w.Pool.Exec(ctx, `INSERT INTO discovery_targets(organization_id,id,target_type,identity_quality,name) VALUES($1,$2,'repository','persistent',$3) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,current=true`, orgID, targetID, owner+"/"+repository)
	if err != nil {
		return err
	}
	_, err = w.Pool.Exec(ctx, `INSERT INTO sources(organization_id,id,target_id,source_type,name,last_sequence,last_full_sequence) VALUES($1,$2,$2,'repository',$3,0,0) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,revoked_at=NULL,target_id=EXCLUDED.target_id`, orgID, sourceID, owner+"/"+repository)
	if err != nil {
		return err
	}
	_, err = w.Pool.Exec(ctx, `INSERT INTO github_repositories(installation_id,organization_id,owner,repository,source_id,last_seen_at) VALUES($1,$2,$3,$4,$5,now()) ON CONFLICT(installation_id,owner,repository) DO UPDATE SET source_id=EXCLUDED.source_id,last_seen_at=now()`, installationID, orgID, strings.ToLower(owner), strings.ToLower(repository), sourceID)
	if err != nil {
		return err
	}
	if err := connectGitHubEnvironment(ctx, w.Pool, orgID, installationID, owner, repository, sourceID, targetID); err != nil {
		return err
	}
	snapshot, err := repositoryscanner.Scan(ctx, repositoryscanner.Options{OrganizationID: orgID, SourceID: sourceID, TargetID: targetID, Root: temporary, RepositoryURL: repositoryURL, CommitSHA: commit})
	if err != nil {
		return err
	}
	snapshot.Sequence = 0
	snapshot.Full = true
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = w.Pool.Exec(ctx, `INSERT INTO ingestion_jobs(id,organization_id,source_id,snapshot_id,status,payload) VALUES($1,$2,$3,$4,'pending',$5) ON CONFLICT(organization_id,snapshot_id) DO NOTHING`, uuid.New(), orgID, sourceID, snapshot.SnapshotID, payload)
	return err
}

func connectGitHubEnvironment(ctx context.Context, pool *pgxpool.Pool, organizationID string, installationID int64, owner, repository, sourceID, targetID string) error {
	externalID := strings.ToLower(owner + "/" + repository)
	var environmentID uuid.UUID
	err := pool.QueryRow(ctx, `UPDATE environment_connections SET external_id=$3,display_name=$4,connection_status='connected',source_id=$5,target_id=$6,schedule_enabled=false,verified_at=COALESCE(verified_at,now()),last_error_code=NULL,last_error_message=NULL,updated_at=now()
		WHERE organization_id=$1 AND id=(SELECT id FROM environment_connections WHERE organization_id=$1 AND kind='github_repository' AND (configuration->>'installation_id')::bigint=$2 AND connection_status<>'disconnected' AND (external_id IS NULL OR lower(external_id)=$3) ORDER BY external_id NULLS FIRST LIMIT 1) RETURNING id`, organizationID, installationID, externalID, owner+"/"+repository, sourceID, targetID).Scan(&environmentID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	configuration := jsonBytes(map[string]any{"installation_id": installationID})
	_, err = pool.Exec(ctx, `INSERT INTO environment_connections(id,organization_id,kind,provider,external_id,display_name,connection_status,configuration,target_id,source_id,schedule_enabled,verified_at,created_by) VALUES($1,$2,'github_repository','github',$3,$4,'connected',$5,$6,$7,false,now(),'github-app') ON CONFLICT DO NOTHING`, uuid.New(), organizationID, externalID, owner+"/"+repository, configuration, targetID, sourceID)
	return err
}
