-- Graph investigation paths must stay bounded independently of total inventory
-- size. Separate directional indexes let PostgreSQL build a BitmapOr for a
-- one-hop expansion, while the GIN index supports membership lookups in the
-- intentionally denormalized evidence subject array.
CREATE INDEX IF NOT EXISTS relationships_current_from_page
    ON relationships (organization_id,from_entity,last_seen_at DESC,id DESC)
    WHERE current=true;

CREATE INDEX IF NOT EXISTS relationships_current_to_page
    ON relationships (organization_id,to_entity,last_seen_at DESC,id DESC)
    WHERE current=true;

CREATE INDEX IF NOT EXISTS evidence_observations_entity_ids
    ON evidence_observations USING gin (entity_ids);

CREATE INDEX IF NOT EXISTS changes_recent_page
    ON changes (organization_id,changed_at DESC,id DESC);
