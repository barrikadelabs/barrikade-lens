-- Evidence observations are an append-only audit history. Reconstructing the
-- latest observation for every detector signature from that history made a
-- graph drill-down grow with 90 days of scans. Keep a compact current
-- projection at ingestion time so reads grow with the current graph instead.
CREATE TABLE IF NOT EXISTS current_evidence_observations (
    organization_id text NOT NULL,
    source_id text NOT NULL,
    detector_id text NOT NULL,
    method text NOT NULL,
    family text NOT NULL,
    locator_key text NOT NULL,
    snapshot_id uuid NOT NULL,
    evidence_id text NOT NULL,
    detector_version text NOT NULL,
    specificity text NOT NULL,
    locator text,
    content_hash text,
    entity_ids text[] NOT NULL DEFAULT '{}',
    observed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    observations bigint NOT NULL DEFAULT 1 CHECK (observations > 0),
    PRIMARY KEY (organization_id,source_id,detector_id,method,family,locator_key),
    FOREIGN KEY (organization_id,source_id)
        REFERENCES sources(organization_id,id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id,snapshot_id,evidence_id)
        REFERENCES evidence_observations(organization_id,snapshot_id,evidence_id) ON DELETE CASCADE
);

ALTER TABLE current_evidence_observations ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS lens_tenant_isolation ON current_evidence_observations;
CREATE POLICY lens_tenant_isolation ON current_evidence_observations
    USING (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    )
    WITH CHECK (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    );

CREATE OR REPLACE FUNCTION project_current_evidence_observation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO current_evidence_observations AS current (
        organization_id,source_id,detector_id,method,family,locator_key,
        snapshot_id,evidence_id,detector_version,specificity,locator,
        content_hash,entity_ids,observed_at,expires_at,observations
    ) VALUES (
        NEW.organization_id,NEW.source_id,NEW.detector_id,NEW.method,NEW.family,COALESCE(NEW.locator,''),
        NEW.snapshot_id,NEW.evidence_id,NEW.detector_version,NEW.specificity,NEW.locator,
        NEW.content_hash,NEW.entity_ids,NEW.observed_at,NEW.expires_at,1
    )
    ON CONFLICT (organization_id,source_id,detector_id,method,family,locator_key)
    DO UPDATE SET
        snapshot_id = CASE WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text) THEN EXCLUDED.snapshot_id ELSE current.snapshot_id END,
        evidence_id = CASE WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text) THEN EXCLUDED.evidence_id ELSE current.evidence_id END,
        detector_version = CASE WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text) THEN EXCLUDED.detector_version ELSE current.detector_version END,
        specificity = CASE WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text) THEN EXCLUDED.specificity ELSE current.specificity END,
        locator = CASE WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text) THEN EXCLUDED.locator ELSE current.locator END,
        content_hash = CASE WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text) THEN EXCLUDED.content_hash ELSE current.content_hash END,
        entity_ids = CASE WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text) THEN EXCLUDED.entity_ids ELSE current.entity_ids END,
        observed_at = GREATEST(current.observed_at,EXCLUDED.observed_at),
        expires_at = CASE
            WHEN current.evidence_id=EXCLUDED.evidence_id
              AND current.detector_version=EXCLUDED.detector_version
              AND current.specificity=EXCLUDED.specificity
              AND current.content_hash IS NOT DISTINCT FROM EXCLUDED.content_hash
              AND current.entity_ids=EXCLUDED.entity_ids
            THEN GREATEST(current.expires_at,EXCLUDED.expires_at)
            WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text)
            THEN EXCLUDED.expires_at ELSE current.expires_at END,
        observations = CASE
            WHEN current.evidence_id=EXCLUDED.evidence_id
              AND current.detector_version=EXCLUDED.detector_version
              AND current.specificity=EXCLUDED.specificity
              AND current.content_hash IS NOT DISTINCT FROM EXCLUDED.content_hash
              AND current.entity_ids=EXCLUDED.entity_ids
            THEN current.observations+1
            WHEN (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text)
            THEN 1 ELSE current.observations END
    WHERE current.evidence_id=EXCLUDED.evidence_id
          AND current.detector_version=EXCLUDED.detector_version
          AND current.specificity=EXCLUDED.specificity
          AND current.content_hash IS NOT DISTINCT FROM EXCLUDED.content_hash
          AND current.entity_ids=EXCLUDED.entity_ids
       OR (EXCLUDED.observed_at,EXCLUDED.evidence_id,EXCLUDED.snapshot_id::text) > (current.observed_at,current.evidence_id,current.snapshot_id::text);
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS evidence_observations_project_current ON evidence_observations;
CREATE TRIGGER evidence_observations_project_current
AFTER INSERT ON evidence_observations
FOR EACH ROW EXECUTE FUNCTION project_current_evidence_observation();

-- Backfill with the same version-selection semantics used by the former read
-- query. The trigger is installed first, so every subsequent observation is
-- projected without relying on a particular ingestion code path.
WITH grouped AS (
    SELECT organization_id,evidence_id,source_id,detector_id,detector_version,method,family,specificity,
           locator,content_hash,entity_ids,max(observed_at) AS observed_at,
           (array_agg(expires_at ORDER BY observed_at DESC,snapshot_id DESC))[1] AS expires_at,
           count(*) AS observations,
           (array_agg(snapshot_id ORDER BY observed_at DESC,snapshot_id DESC))[1] AS snapshot_id
    FROM evidence_observations
    GROUP BY organization_id,evidence_id,source_id,detector_id,detector_version,method,family,specificity,locator,content_hash,entity_ids
), ranked AS (
    SELECT grouped.*,
           row_number() OVER (
               PARTITION BY organization_id,source_id,detector_id,method,family,COALESCE(locator,'')
               ORDER BY observed_at DESC,evidence_id DESC,snapshot_id DESC
           ) AS version_rank
    FROM grouped
)
INSERT INTO current_evidence_observations (
    organization_id,source_id,detector_id,method,family,locator_key,
    snapshot_id,evidence_id,detector_version,specificity,locator,
    content_hash,entity_ids,observed_at,expires_at,observations
)
SELECT organization_id,source_id,detector_id,method,family,COALESCE(locator,''),
       snapshot_id,evidence_id,detector_version,specificity,locator,
       content_hash,entity_ids,observed_at,expires_at,observations
FROM ranked
WHERE version_rank=1
ON CONFLICT (organization_id,source_id,detector_id,method,family,locator_key)
DO NOTHING;

-- Build secondary indexes after the bulk backfill so deployment does not pay
-- per-row GIN maintenance while constructing the initial projection.
CREATE INDEX IF NOT EXISTS current_evidence_entity_ids
    ON current_evidence_observations USING gin (entity_ids);

CREATE INDEX IF NOT EXISTS current_evidence_recent
    ON current_evidence_observations (organization_id,observed_at DESC,evidence_id DESC);

ANALYZE current_evidence_observations;
