package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/exporter"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) listEntities(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
	kind := r.URL.Query().Get("kind")
	confidence := r.URL.Query().Get("confidence")
	includeRemoved := r.URL.Query().Get("include_removed") == "true"
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "last_seen"
	}
	if sortBy != "last_seen" && sortBy != "name" {
		writeError(w, 400, "invalid_sort", "Sort must be last_seen or name")
		return
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), sortBy)
	if err != nil {
		writeError(w, 400, "invalid_cursor", err.Error())
		return
	}
	query := `SELECT e.id,e.kind,e.canonical_key,e.name,e.attributes,e.confidence,e.provenance,e.current,e.stale,e.first_seen_at,e.last_seen_at,p.target_id,p.surface,p.system_role,p.system_type,p.discovery_state,p.network_scope,p.attributed,p.product_id,p.product_category FROM entities e LEFT JOIN entity_posture p ON p.organization_id=e.organization_id AND p.entity_id=e.id WHERE e.organization_id=$1`
	args := []any{principal.OrganizationID}
	index := 2
	if !includeRemoved {
		query += ` AND e.current=true`
	}
	if kind != "" {
		query += fmt.Sprintf(` AND e.kind=$%d`, index)
		args = append(args, kind)
		index++
	}
	if confidence != "" {
		query += fmt.Sprintf(` AND e.confidence=$%d`, index)
		args = append(args, confidence)
		index++
	}
	for _, filter := range []struct{ Param, Column string }{{"state", "p.discovery_state"}, {"surface", "p.surface"}, {"target_id", "p.target_id"}, {"system_role", "p.system_role"}, {"system_type", "p.system_type"}, {"network_scope", "p.network_scope"}} {
		if value := r.URL.Query().Get(filter.Param); value != "" {
			query += fmt.Sprintf(` AND `+filter.Column+`=$%d`, index)
			args = append(args, value)
			index++
		}
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		query += fmt.Sprintf(` AND (e.name ILIKE '%%'||$%d||'%%' OR COALESCE(e.canonical_key,'') ILIKE '%%'||$%d||'%%')`, index, index)
		args = append(args, search)
		index++
	}
	if cursor.ID != "" {
		if sortBy == "name" {
			query += fmt.Sprintf(` AND (lower(e.name),e.id)>($%d,$%d)`, index, index+1)
			args = append(args, cursor.Value, cursor.ID)
			index += 2
		} else {
			when, parseErr := time.Parse(time.RFC3339Nano, cursor.Value)
			if parseErr != nil {
				writeError(w, 400, "invalid_cursor", "Cursor timestamp is invalid")
				return
			}
			query += fmt.Sprintf(` AND (e.last_seen_at,e.id)<($%d,$%d)`, index, index+1)
			args = append(args, when, cursor.ID)
			index += 2
		}
	}
	if sortBy == "name" {
		query += ` ORDER BY lower(e.name),e.id`
	} else {
		query += ` ORDER BY e.last_seen_at DESC,e.id DESC`
	}
	query += fmt.Sprintf(` LIMIT $%d`, index)
	args = append(args, limit)
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query entities")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var next pageCursor
	for rows.Next() {
		var id, entityKind, name, entityConfidence string
		var canonical, targetID, surface, systemRole, systemType, state, network, productID, productCategory *string
		var attributes []byte
		var provenance []string
		var current, stale bool
		var attributed *bool
		var first, last time.Time
		if err := rows.Scan(&id, &entityKind, &canonical, &name, &attributes, &entityConfidence, &provenance, &current, &stale, &first, &last, &targetID, &surface, &systemRole, &systemType, &state, &network, &attributed, &productID, &productCategory); err != nil {
			writeError(w, 500, "database_error", "Could not read entities")
			return
		}
		item := map[string]any{"id": id, "kind": entityKind, "canonical_key": canonical, "name": name, "attributes": jsonObject(attributes), "confidence": entityConfidence, "provenance": provenance, "current": current, "stale": stale, "first_seen_at": first, "last_seen_at": last, "posture": map[string]any{"target_id": targetID, "surface": surface, "system_role": systemRole, "system_type": systemType, "state": state, "network_scope": network, "attributed": attributed, "product_id": productID, "product_category": productCategory}}
		items = append(items, item)
		next = pageCursor{Sort: sortBy, ID: id, Value: last.Format(time.RFC3339Nano)}
		if sortBy == "name" {
			next.Value = strings.ToLower(name)
		}
	}
	response := map[string]any{"items": items, "limit": limit}
	if len(items) == limit {
		response["next_cursor"] = encodeCursor(next)
	}
	writeJSON(w, 200, response)
}

func (s *Server) getEntity(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	row := s.config.Pool.QueryRow(r.Context(), `SELECT id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at FROM entities WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, r.PathValue("id"))
	item, err := scanEntity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Entity not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not read entity")
		return
	}
	var targetID, surface, systemRole, systemType, state, network, productID, productCategory *string
	var attributed *bool
	postureErr := s.config.Pool.QueryRow(r.Context(), `SELECT target_id,surface,system_role,system_type,discovery_state,network_scope,attributed,product_id,product_category FROM entity_posture WHERE organization_id=$1 AND entity_id=$2`, principal.OrganizationID, r.PathValue("id")).Scan(&targetID, &surface, &systemRole, &systemType, &state, &network, &attributed, &productID, &productCategory)
	if postureErr == nil {
		item["posture"] = map[string]any{"target_id": targetID, "surface": surface, "system_role": systemRole, "system_type": systemType, "state": state, "network_scope": network, "attributed": attributed, "product_id": productID, "product_category": productCategory}
	}
	evidenceRows, err := s.config.Pool.Query(r.Context(), `SELECT evidence_id,source_id,detector_id,detector_version,method,family,specificity,locator,content_hash,max(observed_at),count(*)
		FROM evidence_observations WHERE organization_id=$1 AND $2=ANY(entity_ids)
		GROUP BY evidence_id,source_id,detector_id,detector_version,method,family,specificity,locator,content_hash
		ORDER BY max(observed_at) DESC LIMIT 500`, principal.OrganizationID, r.PathValue("id"))
	if err == nil {
		defer evidenceRows.Close()
		evidence := []map[string]any{}
		for evidenceRows.Next() {
			var id, source, detector, version, method, family, specificity string
			var locator, hash *string
			var observed time.Time
			var observations int64
			if evidenceRows.Scan(&id, &source, &detector, &version, &method, &family, &specificity, &locator, &hash, &observed, &observations) == nil {
				evidence = append(evidence, map[string]any{"id": id, "source_id": source, "detector_id": detector, "detector_version": version, "method": method, "family": family, "specificity": specificity, "locator": locator, "content_hash": hash, "observed_at": observed, "observations": observations})
			}
		}
		item["evidence"] = evidence
	}
	writeJSON(w, 200, item)
}

type rowScanner interface{ Scan(...any) error }

func scanEntity(row rowScanner) (map[string]any, error) {
	var id, kind, name, confidence string
	var canonical *string
	var attributes []byte
	var provenance []string
	var current, stale bool
	var first, last time.Time
	err := row.Scan(&id, &kind, &canonical, &name, &attributes, &confidence, &provenance, &current, &stale, &first, &last)
	if err != nil {
		return nil, err
	}
	var attrs map[string]any
	if err := json.Unmarshal(attributes, &attrs); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "kind": kind, "canonical_key": canonical, "name": name, "attributes": attrs, "confidence": confidence, "provenance": provenance, "current": current, "stale": stale, "first_seen_at": first, "last_seen_at": last}, nil
}

func (s *Server) listRelationships(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), "last_seen")
	if err != nil {
		writeError(w, 400, "invalid_cursor", err.Error())
		return
	}
	kind := r.URL.Query().Get("kind")
	entity := r.URL.Query().Get("entity_id")
	query := `SELECT r.id,r.kind,r.from_entity,r.to_entity,r.attributes,r.confidence,r.current,r.stale,r.first_seen_at,r.last_seen_at,source.name,target.name FROM relationships r JOIN entities source ON source.organization_id=r.organization_id AND source.id=r.from_entity JOIN entities target ON target.organization_id=r.organization_id AND target.id=r.to_entity WHERE r.organization_id=$1 AND r.current=true`
	args := []any{principal.OrganizationID}
	index := 2
	if kind != "" {
		query += fmt.Sprintf(` AND r.kind=$%d`, index)
		args = append(args, kind)
		index++
	}
	if entity != "" {
		query += fmt.Sprintf(` AND (r.from_entity=$%d OR r.to_entity=$%d)`, index, index)
		args = append(args, entity)
		index++
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		query += fmt.Sprintf(` AND (source.name ILIKE '%%'||$%d||'%%' OR target.name ILIKE '%%'||$%d||'%%')`, index, index)
		args = append(args, search)
		index++
	}
	if cursor.ID != "" {
		when, parseErr := time.Parse(time.RFC3339Nano, cursor.Value)
		if parseErr != nil {
			writeError(w, 400, "invalid_cursor", "Cursor timestamp is invalid")
			return
		}
		query += fmt.Sprintf(` AND (r.last_seen_at,r.id)<($%d,$%d)`, index, index+1)
		args = append(args, when, cursor.ID)
		index += 2
	}
	query += fmt.Sprintf(` ORDER BY r.last_seen_at DESC,r.id DESC LIMIT $%d`, index)
	args = append(args, limit)
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query relationships")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var next pageCursor
	for rows.Next() {
		var id, kind, from, to, confidence, sourceName, targetName string
		var attributes []byte
		var current, stale bool
		var first, last time.Time
		if err := rows.Scan(&id, &kind, &from, &to, &attributes, &confidence, &current, &stale, &first, &last, &sourceName, &targetName); err != nil {
			writeError(w, 500, "database_error", "Could not read relationships")
			return
		}
		var attrs map[string]any
		_ = json.Unmarshal(attributes, &attrs)
		items = append(items, map[string]any{"id": id, "kind": kind, "from": from, "to": to, "from_name": sourceName, "to_name": targetName, "attributes": attrs, "confidence": confidence, "current": current, "stale": stale, "first_seen_at": first, "last_seen_at": last})
		next = pageCursor{Sort: "last_seen", Value: last.Format(time.RFC3339Nano), ID: id}
	}
	response := map[string]any{"items": items, "limit": limit}
	if len(items) == limit {
		response["next_cursor"] = encodeCursor(next)
	}
	writeJSON(w, 200, response)
}

func (s *Server) listChanges(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), "changed_at")
	if err != nil {
		writeError(w, 400, "invalid_cursor", err.Error())
		return
	}
	query := `SELECT c.id::text,c.source_id,c.entity_id,c.event_type,c.changed_at,c.snapshot_id::text,c.details,c.category,c.summary,e.name,ep.system_type,ep.surface,ep.target_id
		FROM changes c LEFT JOIN entities e ON e.organization_id=c.organization_id AND e.id=c.entity_id LEFT JOIN entity_posture ep ON ep.organization_id=c.organization_id AND ep.entity_id=c.entity_id WHERE c.organization_id=$1`
	args := []any{principal.OrganizationID}
	for _, filter := range []struct{ Param, Column string }{{"category", "c.category"}, {"event_type", "c.event_type"}, {"target_id", "ep.target_id"}, {"system_type", "ep.system_type"}, {"system_role", "ep.system_role"}, {"surface", "ep.surface"}} {
		if value := r.URL.Query().Get(filter.Param); value != "" {
			args = append(args, value)
			query += fmt.Sprintf(` AND `+filter.Column+`=$%d`, len(args))
		}
	}
	if rawWindow := r.URL.Query().Get("window"); rawWindow != "" {
		_, window, parseErr := parseWindow(rawWindow)
		if parseErr != nil {
			writeError(w, 400, "invalid_window", parseErr.Error())
			return
		}
		args = append(args, time.Now().UTC().Add(-window))
		query += fmt.Sprintf(` AND c.changed_at >= $%d`, len(args))
	}
	if cursor.ID != "" {
		when, parseErr := time.Parse(time.RFC3339Nano, cursor.Value)
		if parseErr != nil {
			writeError(w, 400, "invalid_cursor", "Cursor timestamp is invalid")
			return
		}
		args = append(args, when, cursor.ID)
		query += fmt.Sprintf(` AND (c.changed_at,c.id)<($%d,$%d::uuid)`, len(args)-1, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY c.changed_at DESC,c.id DESC LIMIT $%d`, len(args))
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query changes")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var next pageCursor
	for rows.Next() {
		var id, snapshot, source, entity, event, category, summary string
		var changed time.Time
		var details []byte
		var entityName, systemType, surface, targetID *string
		if rows.Scan(&id, &source, &entity, &event, &changed, &snapshot, &details, &category, &summary, &entityName, &systemType, &surface, &targetID) != nil {
			continue
		}
		var parsed map[string]any
		_ = json.Unmarshal(details, &parsed)
		items = append(items, map[string]any{"id": id, "source_id": source, "entity_id": entity, "entity_name": entityName, "event_type": event, "category": category, "summary": summary, "changed_at": changed, "snapshot_id": snapshot, "details": parsed, "system_type": systemType, "surface": surface, "target_id": targetID})
		next = pageCursor{Sort: "changed_at", Value: changed.Format(time.RFC3339Nano), ID: id}
	}
	response := map[string]any{"items": items, "limit": limit}
	if len(items) == limit {
		response["next_cursor"] = encodeCursor(next)
	}
	writeJSON(w, 200, response)
}

func (s *Server) coverage(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	rows, err := s.config.Pool.Query(r.Context(), `SELECT s.id,s.target_id,s.source_type,s.name,s.platform,s.collector_version,s.last_seen_at,s.last_full_at,s.last_sequence,s.latest_partial,s.latest_error_count,s.latest_coverage,COUNT(se.entity_id) FILTER(WHERE se.current),COUNT(se.entity_id) FILTER(WHERE se.stale AND se.current) FROM sources s LEFT JOIN source_entities se ON se.organization_id=s.organization_id AND se.source_id=s.id WHERE s.organization_id=$1 AND s.revoked_at IS NULL GROUP BY s.organization_id,s.id ORDER BY s.name`, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query coverage")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, targetID, sourceType, name string
		var platform, version *string
		var lastSeen, lastFull *time.Time
		var sequence, current, stale int64
		var partial bool
		var errorCount int
		var latestCoverage []byte
		if rows.Scan(&id, &targetID, &sourceType, &name, &platform, &version, &lastSeen, &lastFull, &sequence, &partial, &errorCount, &latestCoverage, &current, &stale) != nil {
			continue
		}
		items = append(items, map[string]any{"source_id": id, "target_id": targetID, "source_type": sourceType, "name": name, "platform": platform, "collector_version": version, "last_seen_at": lastSeen, "last_full_at": lastFull, "sequence": sequence, "partial": partial, "error_count": errorCount, "latest_coverage": jsonObject(latestCoverage), "current_entities": current, "stale_entities": stale})
	}
	targetTypes := []map[string]any{}
	for _, targetType := range []string{"endpoint", "repository", "kubernetes"} {
		var reporting, fresh, stale, partial int
		var expected *int
		err := s.config.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FILTER(WHERE current AND last_seen_at IS NOT NULL),COUNT(*) FILTER(WHERE current AND last_seen_at >= now()-CASE target_type WHEN 'endpoint' THEN interval '60 minutes' WHEN 'repository' THEN interval '36 hours' ELSE interval '12 hours' END),COUNT(*) FILTER(WHERE current AND last_seen_at IS NOT NULL AND last_seen_at < now()-CASE target_type WHEN 'endpoint' THEN interval '60 minutes' WHEN 'repository' THEN interval '36 hours' ELSE interval '12 hours' END),COUNT(*) FILTER(WHERE current AND EXISTS(SELECT 1 FROM sources s WHERE s.organization_id=discovery_targets.organization_id AND s.target_id=discovery_targets.id AND s.revoked_at IS NULL AND s.latest_partial)),(SELECT expected_count FROM coverage_baselines WHERE organization_id=$1 AND target_type=$2) FROM discovery_targets WHERE organization_id=$1 AND target_type=$2`, principal.OrganizationID, targetType).Scan(&reporting, &fresh, &stale, &partial, &expected)
		if err != nil {
			writeError(w, 500, "database_error", "Could not compute target coverage")
			return
		}
		targetTypes = append(targetTypes, map[string]any{"target_type": targetType, "reporting": reporting, "fresh": fresh, "stale": stale, "partial": partial, "expected_count": expected, "population_configured": expected != nil})
	}
	writeJSON(w, 200, map[string]any{"target_types": targetTypes, "collectors": map[string]any{"active": len(items)}, "sources": items})
}

func (s *Server) exports(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "lens"
	}
	snapshot := discovery.NewSnapshot(principal.OrganizationID, "hub-export", discovery.SourceRepository, discovery.Collector{ID: "lens-hub", Name: "Lens Hub", Version: Version, Mode: "export"})
	snapshot.Scope.Name = "Lens Hub current inventory"
	rows, err := s.config.Pool.Query(r.Context(), `SELECT id,kind,canonical_key,name,attributes,confidence,provenance FROM entities WHERE organization_id=$1 AND current=true ORDER BY id`, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not export entities")
		return
	}
	for rows.Next() {
		var entity discovery.Entity
		var canonical *string
		var attributes []byte
		if err := rows.Scan(&entity.ID, &entity.Kind, &canonical, &entity.Name, &attributes, &entity.Confidence, &entity.Provenance); err != nil {
			rows.Close()
			writeError(w, 500, "database_error", "Could not export entities")
			return
		}
		if canonical != nil {
			entity.CanonicalKey = *canonical
		}
		_ = json.Unmarshal(attributes, &entity.Attributes)
		snapshot.Entities = append(snapshot.Entities, entity)
	}
	rows.Close()
	relationRows, err := s.config.Pool.Query(r.Context(), `SELECT id,kind,from_entity,to_entity,attributes,confidence FROM relationships WHERE organization_id=$1 AND current=true ORDER BY id`, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not export relationships")
		return
	}
	for relationRows.Next() {
		var relation discovery.Relationship
		var attributes []byte
		if relationRows.Scan(&relation.ID, &relation.Kind, &relation.From, &relation.To, &attributes, &relation.Confidence) == nil {
			_ = json.Unmarshal(attributes, &relation.Attributes)
			snapshot.Relationships = append(snapshot.Relationships, relation)
		}
	}
	relationRows.Close()
	snapshot.Coverage.DetectorsRun = 0
	var exportFormat exporter.Format
	switch format {
	case "lens":
		exportFormat = exporter.FormatJSON
	case "ndjson":
		exportFormat = exporter.FormatNDJSON
	case "cyclonedx":
		exportFormat = exporter.FormatCycloneDX
	default:
		writeError(w, 400, "invalid_format", "Format must be lens, ndjson, or cyclonedx")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if exportFormat == exporter.FormatNDJSON {
		w.Header().Set("Content-Type", "application/x-ndjson")
	}
	if err := exporter.Write(w, snapshot, exportFormat); err != nil {
		s.config.Logger.Error("export failed", "error", err)
	}
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "admin:webhooks")
	if err != nil || !principal.Admin {
		writeError(w, 403, "forbidden", "Administrator access is required")
		return
	}
	var request struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	endpoint, err := url.Parse(request.URL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		writeError(w, 400, "invalid_webhook_url", "Webhook URL must be an HTTPS URL without credentials, query parameters, or fragments")
		return
	}
	host := strings.ToLower(endpoint.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || discovery.IsCloudMetadataHost(host) {
		writeError(w, 400, "invalid_webhook_url", "Webhook URL may not target localhost")
		return
	}
	secret, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not create webhook")
		return
	}
	id := uuid.New()
	_, err = s.config.Pool.Exec(r.Context(), `INSERT INTO webhook_endpoints(id,organization_id,url,signing_secret) VALUES($1,$2,$3,$4)`, id, principal.OrganizationID, endpoint.String(), []byte(secret))
	if err != nil {
		writeError(w, 500, "database_error", "Could not create webhook")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "url": endpoint.String(), "signing_secret": secret})
}

var Version = "2.0.0-dev"
