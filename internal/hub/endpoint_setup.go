package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	endpointCredentialLifetime = 15 * time.Minute
	endpointHandoffLifetime    = 24 * time.Hour
	endpointLauncherVersion    = "2.0.6"
)

func (s *Server) getEnvironmentActivation(w http.ResponseWriter, r *http.Request) {
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
	var connection string
	var sourceID, errorCode, errorMessage, resultStatus *string
	var firstResult, lastResult, sourceSeen *time.Time
	var sourcePartial *bool
	err = s.db(r.Context()).QueryRow(r.Context(), `SELECT e.connection_status,e.source_id,e.first_result_at,e.last_result_at,e.last_result_status,e.last_error_code,e.last_error_message,s.last_seen_at,s.latest_partial
		FROM environment_connections e LEFT JOIN sources s ON s.organization_id=e.organization_id AND s.id=e.source_id
		WHERE e.organization_id=$1 AND e.id=$2`, principal.OrganizationID, id).Scan(&connection, &sourceID, &firstResult, &lastResult, &resultStatus, &errorCode, &errorMessage, &sourceSeen, &sourcePartial)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Environment not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not load activation status")
		return
	}
	phase := "awaiting_install"
	switch {
	case connection == "disconnected":
		phase = "disconnected"
	case connection == "auth_error" || resultStatus != nil && *resultStatus == "failed":
		phase = "failed"
	case sourceID == nil:
		phase = "awaiting_install"
	case lastResult == nil:
		var processing bool
		_ = s.db(r.Context()).QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM ingestion_jobs WHERE organization_id=$1 AND source_id=$2 AND status IN ('pending','processing'))`, principal.OrganizationID, *sourceID).Scan(&processing)
		if processing {
			phase = "processing"
		} else {
			phase = "connected"
		}
	case sourceSeen != nil && time.Since(*sourceSeen) > time.Hour:
		phase = "stale"
	case resultStatus != nil && *resultStatus == "partial" || sourcePartial != nil && *sourcePartial:
		phase = "partial"
	default:
		phase = "ready"
	}
	writeJSON(w, 200, map[string]any{
		"environment_id": id, "phase": phase, "connection_status": connection,
		"first_result_at": firstResult, "last_result_at": lastResult,
		"last_result_status": resultStatus, "last_seen_at": sourceSeen,
		"safe_error": map[string]any{"code": errorCode, "message": errorMessage},
	})
}

func (s *Server) rotateEndpointEnrollmentCredential(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	if !s.config.EndpointConnectorEnabled {
		writeError(w, 404, "connector_disabled", "Endpoint onboarding is disabled")
		return
	}
	environmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Environment ID is invalid")
		return
	}
	token, expiresAt, setupID, err := s.issueEndpointCredential(r.Context(), s.db(r.Context()), principal.OrganizationID, environmentID, principal.Subject)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "setup_not_pending", "Only a pending endpoint setup can rotate its credential")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not rotate the endpoint credential")
		return
	}
	writeJSON(w, 201, map[string]any{
		"id": setupID, "environment_id": environmentID, "kind": "endpoint", "expires_at": expiresAt, "token_displayed_once": true,
		"setup": s.environmentSetupInstructions("endpoint", "endpoint", "", "", token, nil),
	})
}

func (s *Server) issueEndpointCredential(ctx context.Context, q database, orgID string, environmentID uuid.UUID, actor string) (string, time.Time, uuid.UUID, error) {
	var kind, status string
	if err := q.QueryRow(ctx, `SELECT kind,connection_status FROM environment_connections WHERE organization_id=$1 AND id=$2 FOR UPDATE`, orgID, environmentID).Scan(&kind, &status); err != nil {
		return "", time.Time{}, uuid.Nil, err
	}
	if kind != "endpoint" || status != "setup_pending" {
		return "", time.Time{}, uuid.Nil, pgx.ErrNoRows
	}
	if _, err := q.Exec(ctx, `UPDATE connector_setup_sessions SET state='cancelled' WHERE organization_id=$1 AND environment_id=$2 AND state='pending'`, orgID, environmentID); err != nil {
		return "", time.Time{}, uuid.Nil, err
	}
	if _, err := q.Exec(ctx, `UPDATE enrollment_codes SET revoked_at=now() WHERE organization_id=$1 AND environment_id=$2 AND revoked_at IS NULL`, orgID, environmentID); err != nil {
		return "", time.Time{}, uuid.Nil, err
	}
	token, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, uuid.Nil, err
	}
	expiresAt := time.Now().UTC().Add(endpointCredentialLifetime)
	hash := tokenHash(normalizeCode(token))
	setupID := uuid.New()
	if _, err = q.Exec(ctx, `INSERT INTO connector_setup_sessions(id,organization_id,environment_id,token_hash,kind,created_by,expires_at) VALUES($1,$2,$3,$4,'endpoint',$5,$6)`, setupID, orgID, environmentID, hash, actor, expiresAt); err != nil {
		return "", time.Time{}, uuid.Nil, err
	}
	if _, err = q.Exec(ctx, `INSERT INTO enrollment_codes(code_hash,organization_id,environment_id,expires_at,uses_remaining,source_type) VALUES($1,$2,$3,$4,1,'endpoint')`, hash, orgID, environmentID, expiresAt); err != nil {
		return "", time.Time{}, uuid.Nil, err
	}
	return token, expiresAt, setupID, nil
}

func (s *Server) createEndpointHandoff(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	if !s.config.EndpointHandoffEnabled || !s.config.EndpointConnectorEnabled {
		writeError(w, 404, "handoff_disabled", "Delegated endpoint setup is disabled")
		return
	}
	environmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Environment ID is invalid")
		return
	}
	var pending bool
	if err = s.db(r.Context()).QueryRow(r.Context(), `SELECT kind='endpoint' AND connection_status='setup_pending' FROM environment_connections WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, environmentID).Scan(&pending); err != nil || !pending {
		writeError(w, 409, "setup_not_pending", "Only a pending endpoint setup can be delegated")
		return
	}
	if _, err = s.db(r.Context()).Exec(r.Context(), `UPDATE endpoint_setup_handoffs SET revoked_at=now() WHERE organization_id=$1 AND environment_id=$2 AND revoked_at IS NULL`, principal.OrganizationID, environmentID); err != nil {
		writeError(w, 500, "database_error", "Could not rotate the setup handoff")
		return
	}
	raw, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not create the setup handoff")
		return
	}
	id := uuid.New()
	expiresAt := time.Now().UTC().Add(endpointHandoffLifetime)
	if _, err = s.db(r.Context()).Exec(r.Context(), `INSERT INTO endpoint_setup_handoffs(id,organization_id,environment_id,token_hash,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, id, principal.OrganizationID, environmentID, tokenHash(raw), principal.Subject, expiresAt); err != nil {
		writeError(w, 500, "database_error", "Could not create the setup handoff")
		return
	}
	base := strings.TrimSuffix(s.config.PublicURL, "/") + "/install#token=" + url.QueryEscape(raw)
	writeJSON(w, 201, map[string]any{"id": id, "environment_id": environmentID, "url": base, "expires_at": expiresAt, "token_displayed_once": true})
}

func (s *Server) revokeEndpointHandoff(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "environment:manage")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	environmentID, firstErr := uuid.Parse(r.PathValue("id"))
	handoffID, secondErr := uuid.Parse(r.PathValue("handoffId"))
	if firstErr != nil || secondErr != nil {
		writeError(w, 400, "invalid_id", "Environment or handoff ID is invalid")
		return
	}
	tag, err := s.db(r.Context()).Exec(r.Context(), `UPDATE endpoint_setup_handoffs SET revoked_at=now() WHERE organization_id=$1 AND environment_id=$2 AND id=$3 AND revoked_at IS NULL`, principal.OrganizationID, environmentID, handoffID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not revoke the setup handoff")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "Active setup handoff not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resolveEndpointHandoff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.config.EndpointHandoffEnabled || !s.config.EndpointConnectorEnabled {
		writeError(w, 404, "handoff_disabled", "Delegated endpoint setup is disabled")
		return
	}
	var request struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := decodeJSON(w, r, &request, 16<<10); err != nil {
		return
	}
	request.Platform = strings.ToLower(strings.TrimSpace(request.Platform))
	if request.Token == "" || request.Platform != "macos" && request.Platform != "windows" && request.Platform != "linux" {
		writeError(w, 400, "invalid_handoff", "A handoff token and supported platform are required")
		return
	}
	tx, err := s.config.WorkerPool.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not open delegated setup")
		return
	}
	defer tx.Rollback(r.Context())
	var handoffID, environmentID uuid.UUID
	var orgID, workspaceName, environmentName string
	err = tx.QueryRow(r.Context(), `SELECT h.id,h.environment_id,h.organization_id,o.name,e.display_name
		FROM endpoint_setup_handoffs h JOIN organizations o ON o.id=h.organization_id
		JOIN environment_connections e ON e.organization_id=h.organization_id AND e.id=h.environment_id
		WHERE h.token_hash=$1 AND h.revoked_at IS NULL AND h.expires_at>now() AND e.kind='endpoint' AND e.connection_status='setup_pending' FOR UPDATE OF h`, tokenHash(request.Token)).Scan(&handoffID, &environmentID, &orgID, &workspaceName, &environmentName)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 410, "handoff_expired", "This delegated setup is expired, revoked, or already completed")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not open delegated setup")
		return
	}
	token, expiresAt, _, err := s.issueEndpointCredential(r.Context(), tx, orgID, environmentID, "handoff:"+handoffID.String())
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE endpoint_setup_handoffs SET last_viewed_at=now() WHERE id=$1`, handoffID)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 500, "database_error", "Could not generate the delegated setup command")
		return
	}
	setup := s.environmentSetupInstructions("endpoint", "endpoint", "", environmentName, token, nil)
	commands, _ := setup["commands"].(map[string]string)
	writeJSON(w, 200, map[string]any{
		"workspace_name": workspaceName, "environment_name": environmentName,
		"platform": request.Platform, "command": commands[request.Platform], "expires_at": expiresAt,
		"prerequisites":   []string{"Node.js 18 or newer", "Administrator access to install the background collector"},
		"what_lens_reads": setup["what_lens_reads"], "excluded": setup["excluded"],
	})
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	rows, err := s.db(r.Context()).Query(r.Context(), `SELECT id,event_type,payload,read_at,created_at FROM notification_outbox WHERE organization_id=$1 ORDER BY created_at DESC LIMIT 50`, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not load notifications")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var eventType string
		var payload []byte
		var readAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &eventType, &payload, &readAt, &createdAt); err != nil {
			writeError(w, 500, "database_error", "Could not load notifications")
			return
		}
		items = append(items, map[string]any{"id": id, "event_type": eventType, "payload": jsonObject(payload), "read_at": readAt, "created_at": createdAt})
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, "database_error", "Could not load notifications")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) readNotification(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_id", "Notification ID is invalid")
		return
	}
	tag, err := s.db(r.Context()).Exec(r.Context(), `UPDATE notification_outbox SET read_at=COALESCE(read_at,now()) WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, id)
	if err != nil {
		writeError(w, 500, "database_error", "Could not update notification")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "Notification not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func endpointInstallCommand(platform, token, hub string) string {
	command := fmt.Sprintf("npx --yes barrikade-lens@%s enroll %s --hub %s --install", endpointLauncherVersion, shellQuote(token), shellQuote(strings.TrimSuffix(hub, "/")))
	if platform == "windows" {
		command = fmt.Sprintf("npx --yes barrikade-lens@%s enroll %s --hub %s --install", endpointLauncherVersion, powershellQuote(token), powershellQuote(strings.TrimSuffix(hub, "/")))
	}
	return command
}
