-- Bounded topology paths look up the current safe evidence for each edge.
CREATE INDEX IF NOT EXISTS evidence_observations_relationship_ids
    ON evidence_observations USING gin (relationship_ids);
