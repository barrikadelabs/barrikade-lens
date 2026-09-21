ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS surface text;
ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS observation_state text DEFAULT 'discovered';
ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS observed_at timestamptz DEFAULT now();

UPDATE source_relationships sr
SET surface=s.source_type,
    observation_state='discovered',
    observed_at=sr.last_seen_at
FROM sources s
WHERE s.organization_id=sr.organization_id AND s.id=sr.source_id
  AND (sr.surface IS NULL OR sr.observation_state IS NULL OR sr.observed_at IS NULL);

ALTER TABLE source_relationships ALTER COLUMN observation_state SET NOT NULL;
ALTER TABLE source_relationships ALTER COLUMN observed_at SET NOT NULL;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='source_relationships_surface_check') THEN
        ALTER TABLE source_relationships ADD CONSTRAINT source_relationships_surface_check CHECK (surface IN ('endpoint','repository','kubernetes','cloud'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='source_relationships_observation_state_check') THEN
        ALTER TABLE source_relationships ADD CONSTRAINT source_relationships_observation_state_check CHECK (observation_state IN ('declared','discovered','observed'));
    END IF;
END $$;

ALTER TABLE relationships ADD COLUMN IF NOT EXISTS surfaces text[] NOT NULL DEFAULT '{}';
ALTER TABLE relationships ADD COLUMN IF NOT EXISTS observation_states text[] NOT NULL DEFAULT '{}';

UPDATE relationships r
SET surfaces=observed.surfaces, observation_states=observed.observation_states
FROM (
    SELECT sr.organization_id,sr.relationship_id,
           array_agg(DISTINCT COALESCE(sr.surface,s.source_type) ORDER BY COALESCE(sr.surface,s.source_type)) surfaces,
           array_agg(DISTINCT COALESCE(sr.observation_state,'discovered') ORDER BY COALESCE(sr.observation_state,'discovered')) observation_states
    FROM source_relationships sr
    JOIN sources s ON s.organization_id=sr.organization_id AND s.id=sr.source_id
    WHERE sr.current=true
    GROUP BY sr.organization_id,sr.relationship_id
) observed
WHERE r.organization_id=observed.organization_id AND r.id=observed.relationship_id;
