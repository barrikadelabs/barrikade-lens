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
	query := `SELECT id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at FROM entities WHERE organization_id=$1`
	args := []any{principal.OrganizationID}
	index := 2
	if !includeRemoved {
		query += ` AND current=true`
	}
	if kind != "" {
		query += fmt.Sprintf(` AND kind=$%d`, index)
		args = append(args, kind)
		index++
	}
	if confidence != "" {
		query += fmt.Sprintf(` AND confidence=$%d`, index)
		args = append(args, confidence)
		index++
	}
	query += fmt.Sprintf(` ORDER BY last_seen_at DESC,id LIMIT $%d`, index)
	args = append(args, limit)
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query entities")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		item, err := scanEntity(rows)
		if err != nil {
			writeError(w, 500, "database_error", "Could not read entities")
			return
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit})
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
	evidenceRows, err := s.config.Pool.Query(r.Context(), `SELECT evidence_id,source_id,detector_id,detector_version,method,family,specificity,locator,content_hash,observed_at FROM evidence_observations WHERE organization_id=$1 AND $2=ANY(entity_ids) ORDER BY observed_at DESC LIMIT 500`, principal.OrganizationID, r.PathValue("id"))
	if err == nil {
		defer evidenceRows.Close()
		evidence := []map[string]any{}
		for evidenceRows.Next() {
			var id, source, detector, version, method, family, specificity string
			var locator, hash *string
			var observed time.Time
			if evidenceRows.Scan(&id, &source, &detector, &version, &method, &family, &specificity, &locator, &hash, &observed) == nil {
				evidence = append(evidence, map[string]any{"id": id, "source_id": source, "detector_id": detector, "detector_version": version, "method": method, "family": family, "specificity": specificity, "locator": locator, "content_hash": hash, "observed_at": observed})
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
	kind := r.URL.Query().Get("kind")
	entity := r.URL.Query().Get("entity_id")
	query := `SELECT id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at FROM relationships WHERE organization_id=$1 AND current=true`
	args := []any{principal.OrganizationID}
	index := 2
	if kind != "" {
		query += fmt.Sprintf(` AND kind=$%d`, index)
		args = append(args, kind)
		index++
	}
	if entity != "" {
		query += fmt.Sprintf(` AND (from_entity=$%d OR to_entity=$%d)`, index, index)
		args = append(args, entity)
		index++
	}
	query += fmt.Sprintf(` ORDER BY last_seen_at DESC,id LIMIT $%d`, index)
	args = append(args, limit)
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query relationships")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, kind, from, to, confidence string
		var attributes []byte
		var current, stale bool
		var first, last time.Time
		if err := rows.Scan(&id, &kind, &from, &to, &attributes, &confidence, &current, &stale, &first, &last); err != nil {
			writeError(w, 500, "database_error", "Could not read relationships")
			return
		}
		var attrs map[string]any
		_ = json.Unmarshal(attributes, &attrs)
		items = append(items, map[string]any{"id": id, "kind": kind, "from": from, "to": to, "attributes": attrs, "confidence": confidence, "current": current, "stale": stale, "first_seen_at": first, "last_seen_at": last})
	}
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit})
}

func (s *Server) listChanges(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
	rows, err := s.config.Pool.Query(r.Context(), `SELECT id,source_id,entity_id,event_type,changed_at,snapshot_id,details FROM changes WHERE organization_id=$1 ORDER BY changed_at DESC LIMIT $2`, principal.OrganizationID, limit)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query changes")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, snapshot uuid.UUID
		var source, entity, event string
		var changed time.Time
		var details []byte
		if rows.Scan(&id, &source, &entity, &event, &changed, &snapshot, &details) != nil {
			continue
		}
		var parsed map[string]any
		_ = json.Unmarshal(details, &parsed)
		items = append(items, map[string]any{"id": id, "source_id": source, "entity_id": entity, "event_type": event, "changed_at": changed, "snapshot_id": snapshot, "details": parsed})
	}
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit})
}

func (s *Server) coverage(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	rows, err := s.config.Pool.Query(r.Context(), `SELECT s.id,s.source_type,s.name,s.platform,s.collector_version,s.last_seen_at,s.last_full_at,s.last_sequence,COUNT(se.entity_id) FILTER(WHERE se.current),COUNT(se.entity_id) FILTER(WHERE se.stale AND se.current) FROM sources s LEFT JOIN source_entities se ON se.organization_id=s.organization_id AND se.source_id=s.id WHERE s.organization_id=$1 AND s.revoked_at IS NULL GROUP BY s.organization_id,s.id ORDER BY s.name`, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query coverage")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, sourceType, name string
		var platform, version *string
		var lastSeen, lastFull *time.Time
		var sequence, current, stale int64
		if rows.Scan(&id, &sourceType, &name, &platform, &version, &lastSeen, &lastFull, &sequence, &current, &stale) != nil {
			continue
		}
		items = append(items, map[string]any{"source_id": id, "source_type": sourceType, "name": name, "platform": platform, "collector_version": version, "last_seen_at": lastSeen, "last_full_at": lastFull, "sequence": sequence, "current_entities": current, "stale_entities": stale})
	}
	writeJSON(w, 200, map[string]any{"sources": items})
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
