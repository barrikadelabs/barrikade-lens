package hub

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type deviceFleetSummary struct {
	Enrolled     int `json:"enrolled"`
	Scanned      int `json:"scanned"`
	Reporting    int `json:"reporting"`
	StaleOffline int `json:"stale_offline"`
	Partial      int `json:"partial"`
	Failed       int `json:"failed"`
	Revoked      int `json:"revoked"`
}

type fleetPolicySummary struct {
	ID                  uuid.UUID `json:"id"`
	Name                string    `json:"name"`
	Status              string    `json:"status"`
	ExpectedDeviceCount *int      `json:"expected_device_count"`
	EnrolledCount       int       `json:"enrolled_count"`
	RemainingCount      int       `json:"remaining_count"`
}

type fleetDevice struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	ObservedName         string     `json:"observed_name"`
	CustomName           *string    `json:"custom_name,omitempty"`
	Platform             *string    `json:"platform,omitempty"`
	Architecture         *string    `json:"architecture,omitempty"`
	CollectorVersion     *string    `json:"collector_version,omitempty"`
	ReportingMode        string     `json:"reporting_mode"`
	LifecycleStatus      string     `json:"lifecycle_status"`
	Freshness            string     `json:"freshness"`
	IdentityQuality      string     `json:"identity_quality"`
	PossibleDuplicate    bool       `json:"possible_duplicate"`
	Partial              bool       `json:"partial"`
	Failed               bool       `json:"failed"`
	Current              bool       `json:"current"`
	FirstSeenAt          time.Time  `json:"first_seen_at"`
	LastSeenAt           *time.Time `json:"last_seen_at,omitempty"`
	LastFullAt           *time.Time `json:"last_full_at,omitempty"`
	EvidenceExpiresAt    *time.Time `json:"evidence_expires_at,omitempty"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
	DeploymentPolicyID   *uuid.UUID `json:"deployment_policy_id,omitempty"`
	DeploymentPolicyName *string    `json:"deployment_policy_name,omitempty"`
	EvidenceURL          string     `json:"evidence_url"`
}

func (s *Server) listDeviceFleet(w http.ResponseWriter, r *http.Request) {
	s.listDeviceFleetForPolicy(w, r, nil)
}

func (s *Server) listDeploymentPolicyDevices(w http.ResponseWriter, r *http.Request) {
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
	var exists bool
	if err = s.db(r.Context()).QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM deployment_policies WHERE organization_id=$1 AND id=$2)`, principal.OrganizationID, policyID).Scan(&exists); err != nil || !exists {
		writeError(w, http.StatusNotFound, "not_found", "Deployment policy not found")
		return
	}
	s.listDeviceFleetForPolicy(w, r, &policyID)
}

func (s *Server) listDeviceFleetForPolicy(w http.ResponseWriter, r *http.Request, forcedPolicyID *uuid.UUID) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), "device_name")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}

	policyID := forcedPolicyID
	if policyID == nil && strings.TrimSpace(r.URL.Query().Get("policy_id")) != "" {
		parsed, parseErr := uuid.Parse(r.URL.Query().Get("policy_id"))
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_policy_id", "Deployment policy ID is invalid")
			return
		}
		policyID = &parsed
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "reporting" && status != "stale_offline" && status != "never_scanned" && status != "partial" && status != "failed" && status != "revoked" {
		writeError(w, http.StatusBadRequest, "invalid_status", "Status must be reporting, stale_offline, never_scanned, partial, failed, or revoked")
		return
	}

	const activeFresh = `(t.revoked_at IS NULL AND t.current AND t.reporting_mode='continuous' AND t.last_seen_at IS NOT NULL AND t.last_seen_at>=now()-interval '60 minutes')`
	const staleOffline = `(t.revoked_at IS NULL AND t.last_seen_at IS NOT NULL AND (NOT t.current OR t.reporting_mode<>'continuous' OR t.last_seen_at<now()-interval '60 minutes'))`
	base := ` FROM discovery_targets t
		LEFT JOIN deployment_policies p ON p.organization_id=t.organization_id AND p.id=t.deployment_policy_id
		LEFT JOIN LATERAL (
			SELECT collector_version,latest_partial,latest_error_count,last_seen_at,last_full_at
			FROM sources WHERE organization_id=t.organization_id AND target_id=t.id
			ORDER BY (revoked_at IS NULL) DESC,created_at DESC LIMIT 1
		) s ON true
		WHERE t.organization_id=$1 AND t.target_type='endpoint'`
	args := []any{principal.OrganizationID}
	if policyID != nil {
		args = append(args, *policyID)
		base += fmt.Sprintf(` AND t.deployment_policy_id=$%d`, len(args))
	}
	summaryBase := base
	summaryArgs := append([]any{}, args...)
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		args = append(args, search)
		base += fmt.Sprintf(` AND (COALESCE(NULLIF(t.display_name,''),NULLIF(t.observed_name,''),t.name) ILIKE '%%'||$%d||'%%' OR COALESCE(NULLIF(t.observed_name,''),t.name) ILIKE '%%'||$%d||'%%')`, len(args), len(args))
	}
	switch status {
	case "reporting":
		base += ` AND ` + activeFresh
	case "stale_offline":
		base += ` AND ` + staleOffline
	case "never_scanned":
		base += ` AND t.revoked_at IS NULL AND t.last_seen_at IS NULL`
	case "partial":
		base += ` AND COALESCE(s.latest_partial,false)`
	case "failed":
		base += ` AND COALESCE(s.latest_error_count,0)>0 AND s.last_full_at IS NULL`
	case "revoked":
		base += ` AND t.revoked_at IS NOT NULL`
	}

	var summary deviceFleetSummary
	summaryQuery := `SELECT count(*),count(*) FILTER(WHERE t.last_seen_at IS NOT NULL),count(*) FILTER(WHERE ` + activeFresh + `),count(*) FILTER(WHERE ` + staleOffline + `),count(*) FILTER(WHERE COALESCE(s.latest_partial,false)),count(*) FILTER(WHERE COALESCE(s.latest_error_count,0)>0 AND s.last_full_at IS NULL),count(*) FILTER(WHERE t.revoked_at IS NOT NULL)` + summaryBase
	if err = s.db(r.Context()).QueryRow(r.Context(), summaryQuery, summaryArgs...).Scan(&summary.Enrolled, &summary.Scanned, &summary.Reporting, &summary.StaleOffline, &summary.Partial, &summary.Failed, &summary.Revoked); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not summarize device fleet")
		return
	}

	listQuery := `SELECT t.id,COALESCE(NULLIF(t.display_name,''),NULLIF(t.observed_name,''),t.name),COALESCE(NULLIF(t.observed_name,''),t.name),t.display_name,t.platform,t.architecture,s.collector_version,t.reporting_mode,t.identity_quality,t.current,t.first_seen_at,t.last_seen_at,t.last_full_at,t.evidence_expires_at,t.revoked_at,t.deployment_policy_id,p.name,COALESCE(s.latest_partial,false),COALESCE(s.latest_error_count,0)>0 AND s.last_full_at IS NULL,
		EXISTS(SELECT 1 FROM discovery_targets d WHERE d.organization_id=t.organization_id AND d.id<>t.id AND d.target_type='endpoint' AND d.revoked_at IS NULL AND lower(COALESCE(NULLIF(d.observed_name,''),d.name))=lower(COALESCE(NULLIF(t.observed_name,''),t.name)))` + base
	listArgs := append([]any{}, args...)
	if cursor.ID != "" {
		listArgs = append(listArgs, cursor.Value, cursor.ID)
		listQuery += fmt.Sprintf(` AND (lower(COALESCE(NULLIF(t.display_name,''),NULLIF(t.observed_name,''),t.name)),t.id)>($%d,$%d)`, len(listArgs)-1, len(listArgs))
	}
	listQuery += ` ORDER BY lower(COALESCE(NULLIF(t.display_name,''),NULLIF(t.observed_name,''),t.name)),t.id`
	listArgs = append(listArgs, limit)
	listQuery += fmt.Sprintf(` LIMIT $%d`, len(listArgs))
	rows, err := s.db(r.Context()).Query(r.Context(), listQuery, listArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not list device fleet")
		return
	}
	defer rows.Close()
	items := make([]fleetDevice, 0)
	var next pageCursor
	now := time.Now().UTC()
	for rows.Next() {
		var value fleetDevice
		if err = rows.Scan(&value.ID, &value.Name, &value.ObservedName, &value.CustomName, &value.Platform, &value.Architecture, &value.CollectorVersion, &value.ReportingMode, &value.IdentityQuality, &value.Current, &value.FirstSeenAt, &value.LastSeenAt, &value.LastFullAt, &value.EvidenceExpiresAt, &value.RevokedAt, &value.DeploymentPolicyID, &value.DeploymentPolicyName, &value.Partial, &value.Failed, &value.PossibleDuplicate); err != nil {
			writeError(w, http.StatusInternalServerError, "database_error", "Could not read device fleet")
			return
		}
		value.Freshness = freshnessStateForMode("endpoint", value.ReportingMode, value.LastSeenAt, value.EvidenceExpiresAt, now)
		value.LifecycleStatus = deviceLifecycle(value, now)
		value.EvidenceURL = "/v1/entities?target_id=" + value.ID
		items = append(items, value)
		next = pageCursor{Sort: "device_name", Value: strings.ToLower(value.Name), ID: value.ID}
	}
	if err = rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not list device fleet")
		return
	}
	policyQuery := `SELECT p.id,p.name,CASE WHEN p.revoked_at IS NULL THEN 'active' ELSE 'revoked' END,p.expected_device_count,
		(SELECT count(DISTINCT e.target_id) FROM deployment_policy_enrollments e WHERE e.organization_id=p.organization_id AND e.policy_id=p.id),
		COALESCE((SELECT sum(c.uses_remaining) FROM enrollment_codes c WHERE c.organization_id=p.organization_id AND c.policy_id=p.id AND c.revoked_at IS NULL AND c.expires_at>now()),0)
		FROM deployment_policies p WHERE p.organization_id=$1 AND p.source_type='endpoint'`
	policyArgs := []any{principal.OrganizationID}
	if policyID != nil {
		policyArgs = append(policyArgs, *policyID)
		policyQuery += ` AND p.id=$2`
	}
	policyQuery += ` ORDER BY lower(p.name),p.id`
	policyRows, policyErr := s.db(r.Context()).Query(r.Context(), policyQuery, policyArgs...)
	if policyErr != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not list fleet policies")
		return
	}
	policies := make([]fleetPolicySummary, 0)
	for policyRows.Next() {
		var policy fleetPolicySummary
		if policyRows.Scan(&policy.ID, &policy.Name, &policy.Status, &policy.ExpectedDeviceCount, &policy.EnrolledCount, &policy.RemainingCount) != nil {
			policyRows.Close()
			writeError(w, http.StatusInternalServerError, "database_error", "Could not read fleet policies")
			return
		}
		policies = append(policies, policy)
	}
	if policyRows.Err() != nil {
		policyRows.Close()
		writeError(w, http.StatusInternalServerError, "database_error", "Could not list fleet policies")
		return
	}
	policyRows.Close()
	response := map[string]any{"items": items, "summary": summary, "policies": policies, "limit": limit}
	if len(items) == limit {
		response["next_cursor"] = encodeCursor(next)
	}
	writeJSON(w, http.StatusOK, response)
}

func deviceLifecycle(value fleetDevice, now time.Time) string {
	if value.RevokedAt != nil {
		return "revoked"
	}
	if value.LastSeenAt == nil {
		return "never_scanned"
	}
	if value.Failed {
		return "failed"
	}
	if value.Partial {
		return "partial"
	}
	if !value.Current || value.ReportingMode != "continuous" || now.Sub(*value.LastSeenAt) > time.Hour {
		return "stale_offline"
	}
	return "reporting"
}

func (s *Server) getFleetDevice(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	query := `SELECT t.id,COALESCE(NULLIF(t.display_name,''),NULLIF(t.observed_name,''),t.name),COALESCE(NULLIF(t.observed_name,''),t.name),t.display_name,t.platform,t.architecture,s.collector_version,t.reporting_mode,t.identity_quality,t.current,t.first_seen_at,t.last_seen_at,t.last_full_at,t.evidence_expires_at,t.revoked_at,t.deployment_policy_id,p.name,COALESCE(s.latest_partial,false),COALESCE(s.latest_error_count,0)>0 AND s.last_full_at IS NULL,
		EXISTS(SELECT 1 FROM discovery_targets d WHERE d.organization_id=t.organization_id AND d.id<>t.id AND d.target_type='endpoint' AND d.revoked_at IS NULL AND lower(COALESCE(NULLIF(d.observed_name,''),d.name))=lower(COALESCE(NULLIF(t.observed_name,''),t.name)))
		FROM discovery_targets t
		LEFT JOIN deployment_policies p ON p.organization_id=t.organization_id AND p.id=t.deployment_policy_id
		LEFT JOIN LATERAL (SELECT collector_version,latest_partial,latest_error_count,last_full_at FROM sources WHERE organization_id=t.organization_id AND target_id=t.id ORDER BY (revoked_at IS NULL) DESC,created_at DESC LIMIT 1) s ON true
		WHERE t.organization_id=$1 AND t.id=$2 AND t.target_type='endpoint'`
	var value fleetDevice
	err = s.db(r.Context()).QueryRow(r.Context(), query, principal.OrganizationID, r.PathValue("id")).Scan(&value.ID, &value.Name, &value.ObservedName, &value.CustomName, &value.Platform, &value.Architecture, &value.CollectorVersion, &value.ReportingMode, &value.IdentityQuality, &value.Current, &value.FirstSeenAt, &value.LastSeenAt, &value.LastFullAt, &value.EvidenceExpiresAt, &value.RevokedAt, &value.DeploymentPolicyID, &value.DeploymentPolicyName, &value.Partial, &value.Failed, &value.PossibleDuplicate)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Device not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not inspect device")
		return
	}
	now := time.Now().UTC()
	value.Freshness = freshnessStateForMode("endpoint", value.ReportingMode, value.LastSeenAt, value.EvidenceExpiresAt, now)
	value.LifecycleStatus = deviceLifecycle(value, now)
	value.EvidenceURL = "/v1/entities?target_id=" + value.ID
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) renameFleetDevice(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &request, 16<<10); err != nil {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_name", "Device name is required and must be at most 128 characters")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not rename device")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `UPDATE discovery_targets SET display_name=$3 WHERE organization_id=$1 AND id=$2 AND target_type='endpoint'`, principal.OrganizationID, r.PathValue("id"), request.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not rename device")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Device not found")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'device.renamed','discovery_target',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, r.PathValue("id"), jsonBytes(map[string]any{"display_name": request.Name}))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not record device rename")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not rename device")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "name": request.Name})
}

func (s *Server) revokeFleetDevice(w http.ResponseWriter, r *http.Request) {
	principal, allowed := requirePolicyAdmin(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Administrator access is required")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not revoke device")
		return
	}
	defer tx.Rollback(r.Context())
	var targetType string
	err = tx.QueryRow(r.Context(), `SELECT target_type FROM discovery_targets WHERE organization_id=$1 AND id=$2 AND target_type='endpoint' AND revoked_at IS NULL FOR UPDATE`, principal.OrganizationID, r.PathValue("id")).Scan(&targetType)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Active device not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not revoke device")
		return
	}
	sourceRows, err := tx.Query(r.Context(), `SELECT id FROM sources WHERE organization_id=$1 AND target_id=$2 AND revoked_at IS NULL FOR UPDATE`, principal.OrganizationID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not load device collectors")
		return
	}
	sourceIDs := make([]string, 0)
	for sourceRows.Next() {
		var sourceID string
		if sourceRows.Scan(&sourceID) == nil {
			sourceIDs = append(sourceIDs, sourceID)
		}
	}
	sourceRows.Close()
	entityRows, err := tx.Query(r.Context(), `SELECT DISTINCT se.entity_id FROM source_entities se JOIN sources s ON s.organization_id=se.organization_id AND s.id=se.source_id WHERE se.organization_id=$1 AND s.target_id=$2 AND se.current=true`, principal.OrganizationID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not load device observations")
		return
	}
	entityIDs := make([]string, 0)
	for entityRows.Next() {
		var entityID string
		if entityRows.Scan(&entityID) == nil {
			entityIDs = append(entityIDs, entityID)
		}
	}
	entityRows.Close()
	type affectedRelationship struct{ ID, From, To string }
	relationRows, err := tx.Query(r.Context(), `SELECT DISTINCT sr.relationship_id,r.from_entity,r.to_entity FROM source_relationships sr JOIN sources s ON s.organization_id=sr.organization_id AND s.id=sr.source_id JOIN relationships r ON r.organization_id=sr.organization_id AND r.id=sr.relationship_id WHERE sr.organization_id=$1 AND s.target_id=$2 AND sr.current=true`, principal.OrganizationID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not load device relationships")
		return
	}
	relationIDs := make([]affectedRelationship, 0)
	for relationRows.Next() {
		var relation affectedRelationship
		if relationRows.Scan(&relation.ID, &relation.From, &relation.To) == nil {
			relationIDs = append(relationIDs, relation)
		}
	}
	relationRows.Close()
	for _, sourceID := range sourceIDs {
		if _, err = tx.Exec(r.Context(), `DELETE FROM collector_refresh_tokens WHERE organization_id=$1 AND source_id=$2`, principal.OrganizationID, sourceID); err != nil {
			break
		}
		if _, err = tx.Exec(r.Context(), `UPDATE source_entities SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2`, principal.OrganizationID, sourceID); err != nil {
			break
		}
		if _, err = tx.Exec(r.Context(), `UPDATE source_relationships SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2`, principal.OrganizationID, sourceID); err != nil {
			break
		}
	}
	for _, entityID := range entityIDs {
		if err != nil {
			break
		}
		_, err = recomputeEntityFromCurrentObservations(r.Context(), tx, principal.OrganizationID, entityID)
	}
	for _, relationship := range relationIDs {
		if err != nil {
			break
		}
		_, err = recomputeRelationshipFromCurrentObservations(r.Context(), tx, principal.OrganizationID, relationship.ID)
		if err == nil {
			for _, entityID := range []string{relationship.From, relationship.To} {
				if err = refreshEntityPosture(r.Context(), tx, principal.OrganizationID, entityID); err != nil {
					break
				}
			}
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE sources SET revoked_at=COALESCE(revoked_at,now()) WHERE organization_id=$1 AND target_id=$2`, principal.OrganizationID, r.PathValue("id"))
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE discovery_targets SET current=false,revoked_at=now(),revoked_by=$3 WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, r.PathValue("id"), principal.Subject)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO workspace_audit_events(id,organization_id,actor_id,event_type,target_type,target_id,metadata) VALUES($1,$2,$3,'device.revoked','discovery_target',$4,$5)`, uuid.New(), principal.OrganizationID, principal.Subject, r.PathValue("id"), jsonBytes(map[string]any{"source_count": len(sourceIDs), "evidence_retained": true}))
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not revoke device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
