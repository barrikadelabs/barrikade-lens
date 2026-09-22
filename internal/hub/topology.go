package hub

import (
	"net/http"
	"time"
)

// topologyPaths returns at most three evidence-backed hops. The per-node
// fanout limit prevents a large inventory from exploding the recursive query.
func (s *Server) topologyPaths(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	direction := r.URL.Query().Get("direction")
	if direction != "downstream" && direction != "upstream" {
		writeError(w, http.StatusBadRequest, "invalid_direction", "Direction must be downstream or upstream")
		return
	}
	entityID := r.URL.Query().Get("entity_id")
	if entityID == "" {
		writeError(w, http.StatusBadRequest, "missing_entity", "Entity ID is required")
		return
	}
	var exists bool
	if err := s.db(r.Context()).QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM entities WHERE organization_id=$1 AND id=$2 AND current=true)`, principal.OrganizationID, entityID).Scan(&exists); err != nil || !exists {
		writeError(w, http.StatusNotFound, "not_found", "Entity not found")
		return
	}
	rows, err := s.db(r.Context()).Query(r.Context(), `WITH RECURSIVE walk(node_ids,edge_ids,head,depth) AS (
		SELECT ARRAY[$2::text],ARRAY[]::text[],$2::text,0
		UNION ALL
		SELECT walk.node_ids || next.next_id,walk.edge_ids || next.id,next.next_id,walk.depth+1
		FROM walk JOIN LATERAL (
			SELECT r.id,CASE WHEN $3='downstream' THEN r.to_entity ELSE r.from_entity END AS next_id
			FROM relationships r
			WHERE r.organization_id=$1 AND r.current=true AND r.stale=false
			  AND r.kind IN ('runs_on','defined_in','deployed_as','uses','exposes','connects_to','provides','invokes','configured_by','contained_in')
			  AND EXISTS(
				SELECT 1 FROM evidence_observations o
				JOIN sources src ON src.organization_id=o.organization_id AND src.id=o.source_id AND src.revoked_at IS NULL
				JOIN source_relationships sr ON sr.organization_id=o.organization_id AND sr.source_id=o.source_id AND sr.relationship_id=r.id AND sr.current=true AND sr.stale=false
				WHERE o.organization_id=r.organization_id AND o.relationship_ids @> ARRAY[r.id]::text[] AND o.expires_at>=now()
			  )
			  AND CASE WHEN $3='downstream' THEN r.from_entity=walk.head ELSE r.to_entity=walk.head END
			ORDER BY r.last_seen_at DESC,r.id LIMIT 12
		) next ON true
		WHERE walk.depth<3 AND NOT next.next_id=ANY(walk.node_ids)
	)
	SELECT node_ids,edge_ids FROM walk WHERE depth>0 ORDER BY depth DESC,node_ids LIMIT 80`, principal.OrganizationID, entityID, direction)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query topology")
		return
	}
	type path struct{ nodes, edges []string }
	paths := []path{}
	entityIDs, relationIDs := map[string]struct{}{}, map[string]struct{}{}
	for rows.Next() {
		var item path
		if err := rows.Scan(&item.nodes, &item.edges); err != nil {
			rows.Close()
			writeError(w, 500, "database_error", "Could not read topology")
			return
		}
		paths = append(paths, item)
		for _, id := range item.nodes {
			entityIDs[id] = struct{}{}
		}
		for _, id := range item.edges {
			relationIDs[id] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, 500, "database_error", "Could not read topology")
		return
	}
	rows.Close()
	ids := make([]string, 0, len(entityIDs))
	for id := range entityIDs {
		ids = append(ids, id)
	}
	rels := make([]string, 0, len(relationIDs))
	for id := range relationIDs {
		rels = append(rels, id)
	}
	entities := map[string]map[string]any{}
	if len(ids) > 0 {
		entityRows, err := s.db(r.Context()).Query(r.Context(), `SELECT id,kind,name FROM entities WHERE organization_id=$1 AND current=true AND id=ANY($2::text[])`, principal.OrganizationID, ids)
		if err != nil {
			writeError(w, 500, "database_error", "Could not read topology entities")
			return
		}
		for entityRows.Next() {
			var id, kind, name string
			if err := entityRows.Scan(&id, &kind, &name); err != nil {
				entityRows.Close()
				writeError(w, 500, "database_error", "Could not read topology entities")
				return
			}
			entities[id] = map[string]any{"id": id, "kind": kind, "name": name}
		}
		if err := entityRows.Err(); err != nil {
			entityRows.Close()
			writeError(w, 500, "database_error", "Could not read topology entities")
			return
		}
		entityRows.Close()
	}
	relationships := map[string]map[string]any{}
	if len(rels) > 0 {
		relationRows, err := s.db(r.Context()).Query(r.Context(), `SELECT id,kind,confidence,surfaces,observation_states,last_seen_at FROM relationships WHERE organization_id=$1 AND current=true AND stale=false AND id=ANY($2::text[])`, principal.OrganizationID, rels)
		if err != nil {
			writeError(w, 500, "database_error", "Could not read topology relationships")
			return
		}
		for relationRows.Next() {
			var id, kind, confidence string
			var surfaces, states []string
			var observed time.Time
			if err := relationRows.Scan(&id, &kind, &confidence, &surfaces, &states, &observed); err != nil {
				relationRows.Close()
				writeError(w, 500, "database_error", "Could not read topology relationships")
				return
			}
			relationships[id] = map[string]any{"id": id, "kind": kind, "confidence": confidence, "surfaces": surfaces, "observation_states": states, "observed_at": observed}
		}
		if err := relationRows.Err(); err != nil {
			relationRows.Close()
			writeError(w, 500, "database_error", "Could not read topology relationships")
			return
		}
		relationRows.Close()
	}
	// Evidence references are read in one bounded batch. A relationship without
	// current evidence is excluded, rather than represented as a supported path.
	evidence := map[string]map[string]any{}
	if len(rels) > 0 {
		evidenceRows, err := s.db(r.Context()).Query(r.Context(), `SELECT edge.id,ob.evidence_id,ob.source_id,ob.method,ob.observed_at
			FROM unnest($2::text[]) AS edge(id)
			JOIN LATERAL (SELECT o.evidence_id,o.source_id,o.method,o.observed_at FROM evidence_observations o
				JOIN sources source ON source.organization_id=o.organization_id AND source.id=o.source_id AND source.revoked_at IS NULL
				JOIN source_relationships sr ON sr.organization_id=o.organization_id AND sr.source_id=o.source_id AND sr.relationship_id=edge.id AND sr.current=true AND sr.stale=false
				WHERE o.organization_id=$1 AND o.relationship_ids @> ARRAY[edge.id]::text[] AND o.expires_at>=now()
				ORDER BY o.observed_at DESC LIMIT 1) ob ON true`, principal.OrganizationID, rels)
		if err != nil {
			writeError(w, 500, "database_error", "Could not read topology evidence")
			return
		}
		for evidenceRows.Next() {
			var id, evidenceID, sourceID, method string
			var observed time.Time
			if err := evidenceRows.Scan(&id, &evidenceID, &sourceID, &method, &observed); err != nil {
				evidenceRows.Close()
				writeError(w, 500, "database_error", "Could not read topology evidence")
				return
			}
			evidence[id] = map[string]any{"evidence_id": evidenceID, "source_id": sourceID, "method": method, "observed_at": observed}
		}
		if err := evidenceRows.Err(); err != nil {
			evidenceRows.Close()
			writeError(w, 500, "database_error", "Could not read topology evidence")
			return
		}
		evidenceRows.Close()
	}
	items := []map[string]any{}
	for _, path := range paths {
		nodes := []map[string]any{}
		edges := []map[string]any{}
		valid := true
		for _, id := range path.nodes {
			if node := entities[id]; node != nil {
				nodes = append(nodes, node)
			} else {
				valid = false
				break
			}
		}
		for _, id := range path.edges {
			if edge := relationships[id]; edge != nil && evidence[id] != nil {
				copy := map[string]any{}
				for key, value := range edge {
					copy[key] = value
				}
				copy["evidence"] = evidence[id]
				edges = append(edges, copy)
			} else {
				valid = false
				break
			}
		}
		if valid {
			items = append(items, map[string]any{"nodes": nodes, "edges": edges})
		}
	}
	writeJSON(w, 200, map[string]any{"entity_id": entityID, "direction": direction, "paths": items, "limit": 80, "max_hops": 3})
}
