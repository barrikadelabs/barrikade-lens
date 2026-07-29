package hub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type pageCursor struct {
	Sort  string `json:"s"`
	Value string `json:"v"`
	ID    string `json:"i"`
}

func encodeCursor(cursor pageCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeCursor(raw, sort string) (pageCursor, error) {
	if raw == "" {
		return pageCursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return pageCursor{}, fmt.Errorf("invalid cursor")
	}
	var cursor pageCursor
	if json.Unmarshal(decoded, &cursor) != nil || cursor.Sort != sort || cursor.ID == "" {
		return pageCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}

func parseWindow(raw string) (string, time.Duration, error) {
	if raw == "" {
		raw = "7d"
	}
	switch raw {
	case "24h":
		return raw, 24 * time.Hour, nil
	case "7d":
		return raw, 7 * 24 * time.Hour, nil
	case "30d":
		return raw, 30 * 24 * time.Hour, nil
	case "90d":
		return raw, 90 * 24 * time.Hour, nil
	default:
		return "", 0, fmt.Errorf("window must be 24h, 7d, 30d, or 90d")
	}
}

func freshnessState(targetType string, lastSeen *time.Time, now time.Time) string {
	if lastSeen == nil {
		return "never"
	}
	threshold := 60 * time.Minute
	switch targetType {
	case "repository":
		threshold = 36 * time.Hour
	case "kubernetes":
		threshold = 12 * time.Hour
	case "catalog":
		threshold = 24 * time.Hour
	}
	if now.Sub(*lastSeen) > threshold {
		return "stale"
	}
	return "fresh"
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	windowName, window, err := parseWindow(r.URL.Query().Get("window"))
	if err != nil {
		writeError(w, 400, "invalid_window", err.Error())
		return
	}
	orgID := principal.OrganizationID
	now := time.Now().UTC()

	coverage := []map[string]any{}
	for _, targetType := range []string{"endpoint", "repository", "kubernetes"} {
		var reporting, fresh, stale, partial, collectors int
		var expected *int
		err = s.config.Pool.QueryRow(r.Context(), `SELECT
			COUNT(DISTINCT t.id) FILTER(WHERE t.current AND t.last_seen_at IS NOT NULL),
			COUNT(DISTINCT t.id) FILTER(WHERE t.current AND t.last_seen_at IS NOT NULL AND t.last_seen_at >= now()-CASE t.target_type WHEN 'endpoint' THEN interval '60 minutes' WHEN 'repository' THEN interval '36 hours' ELSE interval '12 hours' END),
			COUNT(DISTINCT t.id) FILTER(WHERE t.current AND t.last_seen_at IS NOT NULL AND t.last_seen_at < now()-CASE t.target_type WHEN 'endpoint' THEN interval '60 minutes' WHEN 'repository' THEN interval '36 hours' ELSE interval '12 hours' END),
			COUNT(DISTINCT t.id) FILTER(WHERE t.current AND EXISTS(SELECT 1 FROM sources s2 WHERE s2.organization_id=t.organization_id AND s2.target_id=t.id AND s2.revoked_at IS NULL AND s2.latest_partial)),
			COUNT(DISTINCT s.id) FILTER(WHERE s.revoked_at IS NULL),(SELECT b.expected_count FROM coverage_baselines b WHERE b.organization_id=$1 AND b.target_type=$2)
			FROM discovery_targets t
			LEFT JOIN sources s ON s.organization_id=t.organization_id AND s.target_id=t.id
			WHERE t.organization_id=$1 AND t.target_type=$2`, orgID, targetType).Scan(&reporting, &fresh, &stale, &partial, &collectors, &expected)
		if err != nil {
			writeError(w, 500, "database_error", "Could not compute coverage posture")
			return
		}
		coverage = append(coverage, map[string]any{"target_type": targetType, "reporting": reporting, "fresh": fresh, "stale": stale, "partial": partial, "collectors": collectors, "expected_count": expected, "population_configured": expected != nil})
	}

	systemTypes, err := s.countProjection(r, orgID, "system_type", `system_role='system' AND current=true`)
	if err != nil {
		writeError(w, 500, "database_error", "Could not compute system footprint")
		return
	}
	states, _ := s.countProjection(r, orgID, "discovery_state", `system_role='system' AND current=true`)
	surfaces, _ := s.countProjection(r, orgID, "surface", `system_role='system' AND current=true`)
	confidence, _ := s.countProjection(r, orgID, "confidence", `system_role='system' AND current=true`)

	attention := map[string]int{}
	var newSystems int
	_ = s.config.Pool.QueryRow(r.Context(), `SELECT count(*) FROM entity_posture WHERE organization_id=$1 AND current=true AND system_role='system' AND first_seen_at >= $2`, orgID, now.Add(-window)).Scan(&newSystems)
	attention["newly_discovered_systems"] = newSystems
	for key, query := range map[string]string{
		"non_loopback_services": `SELECT count(*) FROM entity_posture p JOIN entities e ON e.organization_id=p.organization_id AND e.id=p.entity_id WHERE p.organization_id=$1 AND p.current=true AND p.network_scope IN ('network','external') AND e.kind IN ('model_server','mcp_server','api_service')`,
		"unattributed_systems":  `SELECT count(*) FROM entity_posture WHERE organization_id=$1 AND current=true AND system_role='system' AND attributed=false`,
		"possible_only_systems": `SELECT count(*) FROM entity_posture WHERE organization_id=$1 AND current=true AND system_role='system' AND confidence='possible'`,
	} {
		var count int
		_ = s.config.Pool.QueryRow(r.Context(), query, orgID).Scan(&count)
		attention[key] = count
	}
	for key, query := range map[string]string{
		"partial_scans":                 `SELECT count(DISTINCT target_id) FROM sources WHERE organization_id=$1 AND revoked_at IS NULL AND latest_partial=true`,
		"stale_targets":                 `SELECT count(*) FROM discovery_targets WHERE organization_id=$1 AND current=true AND last_seen_at IS NOT NULL AND last_seen_at < now()-CASE target_type WHEN 'endpoint' THEN interval '60 minutes' WHEN 'repository' THEN interval '36 hours' ELSE interval '12 hours' END`,
		"possible_duplicate_identities": `SELECT COALESCE(sum(count),0) FROM (SELECT count(*) FROM discovery_targets WHERE organization_id=$1 AND current=true GROUP BY target_type,lower(name) HAVING count(*)>1) duplicates`,
		"fact_conflicts":                `SELECT count(*) FROM data_quality_conflicts WHERE organization_id=$1 AND resolved_at IS NULL`,
	} {
		var count int
		_ = s.config.Pool.QueryRow(r.Context(), query, orgID).Scan(&count)
		attention[key] = count
	}

	changeRows, err := s.config.Pool.Query(r.Context(), `SELECT c.id::text,c.entity_id,c.event_type,c.category,c.summary,c.changed_at,c.details,e.name,ep.system_type,ep.surface
		FROM changes c LEFT JOIN entities e ON e.organization_id=c.organization_id AND e.id=c.entity_id LEFT JOIN entity_posture ep ON ep.organization_id=c.organization_id AND ep.entity_id=c.entity_id
		WHERE c.organization_id=$1 AND c.changed_at >= $2 AND ep.system_role='system' ORDER BY c.changed_at DESC,c.id DESC LIMIT 8`, orgID, now.Add(-window))
	if err != nil {
		writeError(w, 500, "database_error", "Could not compute change summary")
		return
	}
	changes := []map[string]any{}
	for changeRows.Next() {
		var id, entityID, event, category, summary string
		var changed time.Time
		var details []byte
		var name, systemType, surface *string
		if changeRows.Scan(&id, &entityID, &event, &category, &summary, &changed, &details, &name, &systemType, &surface) == nil {
			changes = append(changes, map[string]any{"id": id, "entity_id": entityID, "entity_name": name, "event_type": event, "category": category, "summary": summary, "changed_at": changed, "details": jsonObject(details), "system_type": systemType, "surface": surface})
		}
	}
	changeRows.Close()

	declarationAlignment := map[string]any{"configured": false, "configured_sources": 0, "counts": map[string]int{
		"matched": 0, "declared_only": 0, "observed_only": 0, "conflict": 0, "stale_sources": 0, "unverified_claims": 0,
	}}
	if !s.config.ARDDisabled {
		declarationAlignment = declarationAlignmentData(r.Context(), s.config.Pool, orgID)
	}
	writeJSON(w, 200, map[string]any{
		"window": windowName, "generated_at": now, "coverage": coverage,
		"footprint": map[string]any{"system_types": systemTypes, "states": states, "surfaces": surfaces},
		"attention": attention, "changes": changes,
		"declaration_alignment": declarationAlignment,
		"data_quality":          map[string]any{"confidence": confidence, "confidence_note": "Confirmed requires an authoritative descriptor or independent high-specificity evidence.", "coverage_note": "Expected population is shown only when an administrator configures a baseline."},
	})
}

func (s *Server) countProjection(r *http.Request, organizationID, column, predicate string) (map[string]int, error) {
	allowed := map[string]bool{"system_type": true, "discovery_state": true, "surface": true, "confidence": true}
	if !allowed[column] {
		return nil, fmt.Errorf("unsupported projection")
	}
	rows, err := s.config.Pool.Query(r.Context(), `SELECT COALESCE(`+column+`,'unknown'),count(*) FROM entity_posture WHERE organization_id=$1 AND `+predicate+` GROUP BY `+column, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, rows.Err()
}

func (s *Server) listSystems(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
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
	query := `SELECT e.id,e.kind,e.name,e.attributes,p.target_id,p.surface,p.system_type,p.product_id,p.product_category,p.discovery_state,p.network_scope,p.attributed,p.confidence,p.first_seen_at,p.last_seen_at,t.name,
			COALESCE(dm.status,'unmatched'),COALESCE(dm.declaration_ids,'{}'::text[])
		FROM entity_posture p
		JOIN entities e ON e.organization_id=p.organization_id AND e.id=p.entity_id
		LEFT JOIN discovery_targets t ON t.organization_id=p.organization_id AND t.id=p.target_id
		LEFT JOIN LATERAL (
			SELECT CASE
				WHEN bool_or(m.status='conflict') THEN 'conflict'
				WHEN bool_or(m.status='linked') THEN 'matched'
				ELSE 'unmatched'
			END AS status,
			array_agg(m.declaration_entity_id ORDER BY m.declaration_entity_id)
				FILTER(WHERE m.status IN ('linked','conflict')) AS declaration_ids
			FROM resource_matches m
			WHERE m.organization_id=p.organization_id AND m.observed_entity_id=p.entity_id
				AND m.status IN ('linked','conflict')
		) dm ON true
		WHERE p.organization_id=$1 AND p.current=true AND p.system_role='system'`
	args := []any{principal.OrganizationID}
	add := func(condition string, value any) {
		args = append(args, value)
		query += fmt.Sprintf(condition, len(args))
	}
	filters := []struct{ Param, Column string }{{"system_type", "p.system_type"}, {"state", "p.discovery_state"}, {"surface", "p.surface"}, {"target_id", "p.target_id"}, {"confidence", "p.confidence"}, {"network_scope", "p.network_scope"}}
	for _, filter := range filters {
		if value := r.URL.Query().Get(filter.Param); value != "" {
			add(` AND `+filter.Column+`=$%d`, value)
		}
	}
	if value := r.URL.Query().Get("attribution"); value != "" {
		if value != "attributed" && value != "unattributed" {
			writeError(w, 400, "invalid_filter", "Attribution must be attributed or unattributed")
			return
		}
		add(` AND p.attributed=$%d`, value == "attributed")
	}
	if value := r.URL.Query().Get("declaration_status"); value != "" {
		switch value {
		case "matched":
			query += ` AND EXISTS(SELECT 1 FROM resource_matches dm WHERE dm.organization_id=p.organization_id AND dm.observed_entity_id=p.entity_id AND dm.status='linked')`
		case "conflict":
			query += ` AND EXISTS(SELECT 1 FROM resource_matches dm WHERE dm.organization_id=p.organization_id AND dm.observed_entity_id=p.entity_id AND dm.status='conflict')`
		case "unmatched":
			query += ` AND NOT EXISTS(SELECT 1 FROM resource_matches dm WHERE dm.organization_id=p.organization_id AND dm.observed_entity_id=p.entity_id AND dm.status IN ('linked','conflict'))`
		default:
			writeError(w, 400, "invalid_filter", "Declaration status must be matched, unmatched, or conflict")
			return
		}
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		args = append(args, search)
		query += fmt.Sprintf(` AND (e.name ILIKE '%%'||$%d||'%%' OR COALESCE(p.product_id,'') ILIKE '%%'||$%d||'%%')`, len(args), len(args))
	}
	if cursor.ID != "" {
		if sortBy == "name" {
			args = append(args, cursor.Value, cursor.ID)
			query += fmt.Sprintf(` AND (lower(e.name),e.id)>($%d,$%d)`, len(args)-1, len(args))
		} else {
			when, parseErr := time.Parse(time.RFC3339Nano, cursor.Value)
			if parseErr != nil {
				writeError(w, 400, "invalid_cursor", "Cursor timestamp is invalid")
				return
			}
			args = append(args, when, cursor.ID)
			query += fmt.Sprintf(` AND (p.last_seen_at,p.entity_id)<($%d,$%d)`, len(args)-1, len(args))
		}
	}
	if sortBy == "name" {
		query += ` ORDER BY lower(e.name),e.id`
	} else {
		query += ` ORDER BY p.last_seen_at DESC,p.entity_id DESC`
	}
	args = append(args, limit)
	query += fmt.Sprintf(` LIMIT $%d`, len(args))
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query systems")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var next pageCursor
	for rows.Next() {
		var id, kind, name, surface, discoveryState, networkScope, confidence string
		var attributes []byte
		var targetID, systemType, productID, productCategory, targetName *string
		var declarationStatus string
		var declarationIDs []string
		var attributed bool
		var firstSeen, lastSeen time.Time
		if err := rows.Scan(&id, &kind, &name, &attributes, &targetID, &surface, &systemType, &productID, &productCategory, &discoveryState, &networkScope, &attributed, &confidence, &firstSeen, &lastSeen, &targetName, &declarationStatus, &declarationIDs); err != nil {
			writeError(w, 500, "database_error", "Could not read systems")
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "name": name, "attributes": jsonObject(attributes), "target_id": targetID, "target_name": targetName, "surface": surface, "system_type": systemType, "product_id": productID, "product_category": productCategory, "state": discoveryState, "network_scope": networkScope, "attributed": attributed, "confidence": confidence, "first_seen_at": firstSeen, "last_seen_at": lastSeen, "declaration_status": declarationStatus, "declaration_ids": declarationIDs})
		next = pageCursor{Sort: sortBy, ID: id, Value: lastSeen.Format(time.RFC3339Nano)}
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

func (s *Server) getSystem(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	id := r.PathValue("id")
	row := s.config.Pool.QueryRow(r.Context(), `SELECT e.id,e.kind,e.name,e.attributes,p.target_id,p.surface,p.system_type,p.product_id,p.product_category,p.discovery_state,p.network_scope,p.attributed,p.confidence,p.first_seen_at,p.last_seen_at,t.name
		FROM entity_posture p JOIN entities e ON e.organization_id=p.organization_id AND e.id=p.entity_id LEFT JOIN discovery_targets t ON t.organization_id=p.organization_id AND t.id=p.target_id
		WHERE p.organization_id=$1 AND p.entity_id=$2 AND p.system_role='system'`, principal.OrganizationID, id)
	var entityID, kind, name, surface, state, network, confidence string
	var attributes []byte
	var targetID, systemType, productID, productCategory, targetName *string
	var attributed bool
	var firstSeen, lastSeen time.Time
	err = row.Scan(&entityID, &kind, &name, &attributes, &targetID, &surface, &systemType, &productID, &productCategory, &state, &network, &attributed, &confidence, &firstSeen, &lastSeen, &targetName)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "System not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not read system")
		return
	}
	result := map[string]any{"id": entityID, "kind": kind, "name": name, "attributes": jsonObject(attributes), "target_id": targetID, "target_name": targetName, "surface": surface, "system_type": systemType, "product_id": productID, "product_category": productCategory, "state": state, "network_scope": network, "attributed": attributed, "confidence": confidence, "first_seen_at": firstSeen, "last_seen_at": lastSeen}
	declarationStatus, declarationIDs := s.systemDeclarationInfo(r.Context(), principal.OrganizationID, id)
	result["declaration_status"] = declarationStatus
	result["declaration_ids"] = declarationIDs

	rows, err := s.config.Pool.Query(r.Context(), `SELECT r.id,r.kind,r.from_entity,r.to_entity,r.attributes,r.confidence,e.id,e.kind,e.name,e.attributes
		FROM relationships r JOIN entities e ON e.organization_id=r.organization_id AND e.id=CASE WHEN r.from_entity=$2 THEN r.to_entity ELSE r.from_entity END
		WHERE r.organization_id=$1 AND r.current=true AND (r.from_entity=$2 OR r.to_entity=$2) ORDER BY r.kind,e.name LIMIT 500`, principal.OrganizationID, id)
	connections := []map[string]any{}
	if err == nil {
		for rows.Next() {
			var relationID, relationKind, from, to, relationConfidence, connectedID, connectedKind, connectedName string
			var relationAttributes, connectedAttributes []byte
			if rows.Scan(&relationID, &relationKind, &from, &to, &relationAttributes, &relationConfidence, &connectedID, &connectedKind, &connectedName, &connectedAttributes) == nil {
				label := relationKind
				if relationKind == "owned_by" {
					attrs := jsonObject(relationAttributes)
					if authoritative, _ := attrs["authoritative"].(bool); !authoritative {
						label = "observed_user"
					}
				}
				connections = append(connections, map[string]any{"relationship_id": relationID, "relationship_kind": relationKind, "label": label, "direction": map[bool]string{true: "outgoing", false: "incoming"}[from == id], "confidence": relationConfidence, "attributes": jsonObject(relationAttributes), "entity": map[string]any{"id": connectedID, "kind": connectedKind, "name": connectedName, "attributes": jsonObject(connectedAttributes)}})
			}
		}
		rows.Close()
	}
	result["connections"] = connections
	evidenceRows, _ := s.config.Pool.Query(r.Context(), `SELECT evidence_id,source_id,detector_id,detector_version,method,family,specificity,locator,content_hash,max(observed_at),count(*)
		FROM evidence_observations WHERE organization_id=$1 AND $2=ANY(entity_ids)
		GROUP BY evidence_id,source_id,detector_id,detector_version,method,family,specificity,locator,content_hash
		ORDER BY max(observed_at) DESC LIMIT 250`, principal.OrganizationID, id)
	evidence := []map[string]any{}
	if evidenceRows != nil {
		for evidenceRows.Next() {
			var evidenceID, sourceID, detectorID, detectorVersion, method, family, specificity string
			var locator, hash *string
			var observedAt time.Time
			var observations int64
			if evidenceRows.Scan(&evidenceID, &sourceID, &detectorID, &detectorVersion, &method, &family, &specificity, &locator, &hash, &observedAt, &observations) == nil {
				evidence = append(evidence, map[string]any{"id": evidenceID, "source_id": sourceID, "detector_id": detectorID, "detector_version": detectorVersion, "method": method, "family": family, "specificity": specificity, "locator": locator, "content_hash": hash, "observed_at": observedAt, "observations": observations})
			}
		}
		evidenceRows.Close()
	}
	result["evidence"] = evidence
	writeJSON(w, 200, result)
}

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), "target_name")
	if err != nil {
		writeError(w, 400, "invalid_cursor", err.Error())
		return
	}
	query := `SELECT t.id,t.target_type,t.identity_quality,t.name,t.platform,t.architecture,t.first_seen_at,t.last_seen_at,t.last_full_at,t.current,
		COALESCE(jsonb_agg(jsonb_build_object('source_id',s.id,'source_type',s.source_type,'name',s.name,'collector_version',s.collector_version,'last_seen_at',s.last_seen_at,'last_full_at',s.last_full_at,'sequence',s.last_sequence,'partial',s.latest_partial,'error_count',s.latest_error_count,'coverage',s.latest_coverage) ORDER BY s.created_at) FILTER(WHERE s.id IS NOT NULL),'[]'::jsonb),
		EXISTS(SELECT 1 FROM discovery_targets d WHERE d.organization_id=t.organization_id AND d.id<>t.id AND d.target_type=t.target_type AND d.current=true AND lower(d.name)=lower(t.name))
		FROM discovery_targets t LEFT JOIN sources s ON s.organization_id=t.organization_id AND s.target_id=t.id AND s.revoked_at IS NULL WHERE t.organization_id=$1`
	args := []any{principal.OrganizationID}
	if targetType := r.URL.Query().Get("target_type"); targetType != "" {
		args = append(args, targetType)
		query += fmt.Sprintf(` AND t.target_type=$%d`, len(args))
	}
	if includeCatalog := r.URL.Query().Get("include_catalog"); includeCatalog == "false" {
		query += ` AND t.target_type<>'catalog'`
	} else if includeCatalog != "" && includeCatalog != "true" {
		writeError(w, 400, "invalid_filter", "include_catalog must be true or false")
		return
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		args = append(args, search)
		query += fmt.Sprintf(` AND t.name ILIKE '%%'||$%d||'%%'`, len(args))
	}
	if cursor.ID != "" {
		args = append(args, cursor.Value, cursor.ID)
		query += fmt.Sprintf(` AND (lower(t.name),t.id)>($%d,$%d)`, len(args)-1, len(args))
	}
	query += ` GROUP BY t.organization_id,t.id ORDER BY lower(t.name),t.id`
	args = append(args, limit)
	query += fmt.Sprintf(` LIMIT $%d`, len(args))
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query targets")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var next pageCursor
	now := time.Now().UTC()
	for rows.Next() {
		var id, targetType, identityQuality, name string
		var platform, architecture *string
		var firstSeen time.Time
		var lastSeen, lastFull *time.Time
		var current, duplicate bool
		var collectors []byte
		if err := rows.Scan(&id, &targetType, &identityQuality, &name, &platform, &architecture, &firstSeen, &lastSeen, &lastFull, &current, &collectors, &duplicate); err != nil {
			writeError(w, 500, "database_error", "Could not read targets")
			return
		}
		var nested []map[string]any
		_ = json.Unmarshal(collectors, &nested)
		partial := false
		for _, source := range nested {
			if value, _ := source["partial"].(bool); value {
				partial = true
			}
		}
		items = append(items, map[string]any{"id": id, "target_type": targetType, "identity_quality": identityQuality, "name": name, "platform": platform, "architecture": architecture, "first_seen_at": firstSeen, "last_seen_at": lastSeen, "last_full_at": lastFull, "current": current, "freshness": freshnessState(targetType, lastSeen, now), "partial": partial, "possible_duplicate": duplicate, "collectors": nested})
		next = pageCursor{Sort: "target_name", Value: strings.ToLower(name), ID: id}
	}
	response := map[string]any{"items": items, "limit": limit}
	if len(items) == limit {
		response["next_cursor"] = encodeCursor(next)
	}
	writeJSON(w, 200, response)
}

func (s *Server) getTarget(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	var id, targetType, identityQuality, name string
	var platform, architecture *string
	var firstSeen time.Time
	var lastSeen, lastFull *time.Time
	var current bool
	err = s.config.Pool.QueryRow(r.Context(), `SELECT id,target_type,identity_quality,name,platform,architecture,first_seen_at,last_seen_at,last_full_at,current FROM discovery_targets WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, r.PathValue("id")).Scan(&id, &targetType, &identityQuality, &name, &platform, &architecture, &firstSeen, &lastSeen, &lastFull, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Target not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not read target")
		return
	}
	collectorRows, _ := s.config.Pool.Query(r.Context(), `SELECT id,source_type,name,collector_version,last_sequence,last_seen_at,last_full_at,latest_partial,latest_error_count,latest_coverage,revoked_at FROM sources WHERE organization_id=$1 AND target_id=$2 ORDER BY created_at`, principal.OrganizationID, id)
	collectors := []map[string]any{}
	if collectorRows != nil {
		for collectorRows.Next() {
			var sourceID, sourceType, sourceName string
			var version *string
			var sequence int64
			var sourceLastSeen, sourceLastFull, revoked *time.Time
			var partial bool
			var errorCount int
			var coverage []byte
			if collectorRows.Scan(&sourceID, &sourceType, &sourceName, &version, &sequence, &sourceLastSeen, &sourceLastFull, &partial, &errorCount, &coverage, &revoked) == nil {
				collectors = append(collectors, map[string]any{"source_id": sourceID, "source_type": sourceType, "name": sourceName, "collector_version": version, "sequence": sequence, "last_seen_at": sourceLastSeen, "last_full_at": sourceLastFull, "partial": partial, "error_count": errorCount, "coverage": jsonObject(coverage), "revoked_at": revoked})
			}
		}
		collectorRows.Close()
	}
	writeJSON(w, 200, map[string]any{"id": id, "target_type": targetType, "identity_quality": identityQuality, "name": name, "platform": platform, "architecture": architecture, "first_seen_at": firstSeen, "last_seen_at": lastSeen, "last_full_at": lastFull, "current": current, "freshness": freshnessState(targetType, lastSeen, time.Now().UTC()), "collectors": collectors})
}

func (s *Server) putCoverageBaselines(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "admin:coverage")
	if err != nil || !principal.Admin {
		writeError(w, 403, "forbidden", "Administrator access is required")
		return
	}
	var request struct {
		Baselines []struct {
			TargetType    string `json:"target_type"`
			ExpectedCount *int   `json:"expected_count"`
		} `json:"baselines"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if len(request.Baselines) == 0 || len(request.Baselines) > 3 {
		writeError(w, 400, "invalid_baselines", "Provide one baseline per target type")
		return
	}
	tx, err := s.config.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not update baselines")
		return
	}
	defer tx.Rollback(r.Context())
	seen := map[string]bool{}
	for _, baseline := range request.Baselines {
		if seen[baseline.TargetType] || (baseline.TargetType != "endpoint" && baseline.TargetType != "repository" && baseline.TargetType != "kubernetes") || (baseline.ExpectedCount != nil && *baseline.ExpectedCount < 0) {
			writeError(w, 400, "invalid_baselines", "Target types must be unique and counts cannot be negative")
			return
		}
		seen[baseline.TargetType] = true
		if baseline.ExpectedCount == nil {
			_, err = tx.Exec(r.Context(), `DELETE FROM coverage_baselines WHERE organization_id=$1 AND target_type=$2`, principal.OrganizationID, baseline.TargetType)
		} else {
			_, err = tx.Exec(r.Context(), `INSERT INTO coverage_baselines(organization_id,target_type,expected_count,provenance,updated_at) VALUES($1,$2,$3,'manual',now()) ON CONFLICT(organization_id,target_type) DO UPDATE SET expected_count=EXCLUDED.expected_count,provenance='manual',updated_at=now()`, principal.OrganizationID, baseline.TargetType, *baseline.ExpectedCount)
		}
		if err != nil {
			writeError(w, 500, "database_error", "Could not update baselines")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not update baselines")
		return
	}
	writeJSON(w, 200, map[string]any{"baselines": request.Baselines, "provenance": "manual"})
}

func boolQuery(r *http.Request, name string) (*bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	return &value, err
}
