CREATE TABLE IF NOT EXISTS discovery_targets (
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id text NOT NULL,
    target_type text NOT NULL CHECK (target_type IN ('endpoint','repository','kubernetes')),
    identity_fingerprint text,
    identity_public_key bytea,
    identity_quality text NOT NULL DEFAULT 'persistent' CHECK (identity_quality IN ('persistent','legacy_identity')),
    name text NOT NULL,
    platform text,
    architecture text,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz,
    last_full_at timestamptz,
    current boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS discovery_targets_identity ON discovery_targets(organization_id,identity_fingerprint) WHERE identity_fingerprint IS NOT NULL;
CREATE INDEX IF NOT EXISTS discovery_targets_query ON discovery_targets(organization_id,current,target_type,last_seen_at DESC);

ALTER TABLE sources ADD COLUMN IF NOT EXISTS target_id text;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS architecture text;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS latest_coverage jsonb NOT NULL DEFAULT '{}';
ALTER TABLE sources ADD COLUMN IF NOT EXISTS latest_partial boolean NOT NULL DEFAULT false;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS latest_error_count integer NOT NULL DEFAULT 0;

INSERT INTO discovery_targets(organization_id,id,target_type,identity_quality,name,platform,first_seen_at,last_seen_at,last_full_at,current)
SELECT organization_id,'target:legacy:'||id,source_type,'legacy_identity',name,platform,created_at,last_seen_at,last_full_at,revoked_at IS NULL
FROM sources
ON CONFLICT(organization_id,id) DO NOTHING;
UPDATE sources SET target_id='target:legacy:'||id WHERE target_id IS NULL;
ALTER TABLE sources ALTER COLUMN target_id SET NOT NULL;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='sources_target_fk') THEN
        ALTER TABLE sources ADD CONSTRAINT sources_target_fk FOREIGN KEY(organization_id,target_id) REFERENCES discovery_targets(organization_id,id) ON DELETE CASCADE;
    END IF;
END $$;
CREATE UNIQUE INDEX IF NOT EXISTS sources_one_active_per_target ON sources(organization_id,target_id,source_type) WHERE revoked_at IS NULL;

ALTER TABLE source_entities ADD COLUMN IF NOT EXISTS observation_name text;
ALTER TABLE source_entities ADD COLUMN IF NOT EXISTS observation_kind text;
ALTER TABLE source_entities ADD COLUMN IF NOT EXISTS canonical_key text;
ALTER TABLE source_entities ADD COLUMN IF NOT EXISTS attributes jsonb NOT NULL DEFAULT '{}';
ALTER TABLE source_entities ADD COLUMN IF NOT EXISTS confidence text;
ALTER TABLE source_entities ADD COLUMN IF NOT EXISTS provenance text[] NOT NULL DEFAULT '{}';
ALTER TABLE source_entities ADD COLUMN IF NOT EXISTS material_digest text;
UPDATE source_entities se SET observation_name=e.name,observation_kind=e.kind,canonical_key=e.canonical_key,attributes=e.attributes,confidence=e.confidence,provenance=e.provenance
FROM entities e WHERE e.organization_id=se.organization_id AND e.id=se.entity_id AND se.observation_name IS NULL;

ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS observation_kind text;
ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS from_entity text;
ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS to_entity text;
ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS attributes jsonb NOT NULL DEFAULT '{}';
ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS confidence text;
ALTER TABLE source_relationships ADD COLUMN IF NOT EXISTS material_digest text;
UPDATE source_relationships sr SET observation_kind=r.kind,from_entity=r.from_entity,to_entity=r.to_entity,attributes=r.attributes,confidence=r.confidence
FROM relationships r WHERE r.organization_id=sr.organization_id AND r.id=sr.relationship_id AND sr.observation_kind IS NULL;

CREATE TABLE IF NOT EXISTS source_scans (
    organization_id text NOT NULL,
    source_id text NOT NULL,
    target_id text NOT NULL,
    snapshot_id uuid NOT NULL,
    observed_at timestamptz NOT NULL,
    is_full boolean NOT NULL,
    sequence bigint NOT NULL,
    partial boolean NOT NULL,
    coverage jsonb NOT NULL,
    error_count integer NOT NULL,
    material_digest text NOT NULL,
    PRIMARY KEY (organization_id,snapshot_id),
    FOREIGN KEY (organization_id,source_id) REFERENCES sources(organization_id,id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id,target_id) REFERENCES discovery_targets(organization_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS source_scans_history ON source_scans(organization_id,target_id,observed_at DESC);

CREATE TABLE IF NOT EXISTS coverage_baselines (
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    target_type text NOT NULL CHECK (target_type IN ('endpoint','repository','kubernetes')),
    expected_count integer CHECK (expected_count IS NULL OR expected_count >= 0),
    provenance text NOT NULL DEFAULT 'manual' CHECK (provenance IN ('manual')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,target_type)
);

CREATE TABLE IF NOT EXISTS entity_posture (
    organization_id text NOT NULL,
    entity_id text NOT NULL,
    target_id text,
    surface text NOT NULL,
    system_role text NOT NULL CHECK (system_role IN ('system','component','supporting','target','artifact')),
    system_type text CHECK (system_type IS NULL OR system_type IN ('autonomous_agent','agent_tool','model_runtime')),
    product_id text,
    product_category text,
    discovery_state text NOT NULL CHECK (discovery_state IN ('running','deployed','defined','configured','installed','residual','cached','observed')),
    network_scope text NOT NULL CHECK (network_scope IN ('loopback','network','external','none','unknown')),
    attributed boolean NOT NULL DEFAULT false,
    confidence text NOT NULL,
    current boolean NOT NULL DEFAULT true,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    material_digest text NOT NULL,
    PRIMARY KEY (organization_id,entity_id),
    FOREIGN KEY (organization_id,entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id,target_id) REFERENCES discovery_targets(organization_id,id)
);
CREATE INDEX IF NOT EXISTS entity_posture_overview ON entity_posture(organization_id,current,system_role,system_type,discovery_state);
CREATE INDEX IF NOT EXISTS entity_posture_attention ON entity_posture(organization_id,current,network_scope,attributed,confidence);
CREATE INDEX IF NOT EXISTS entity_posture_target ON entity_posture(organization_id,target_id,current);

CREATE TABLE IF NOT EXISTS data_quality_conflicts (
    organization_id text NOT NULL,
    entity_id text NOT NULL,
    attribute_path text NOT NULL,
    observed_values jsonb NOT NULL,
    source_ids text[] NOT NULL,
    detected_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    PRIMARY KEY (organization_id,entity_id,attribute_path),
    FOREIGN KEY (organization_id,entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS data_quality_conflicts_open ON data_quality_conflicts(organization_id,detected_at DESC) WHERE resolved_at IS NULL;

ALTER TABLE changes ADD COLUMN IF NOT EXISTS category text NOT NULL DEFAULT 'identity';
ALTER TABLE changes ADD COLUMN IF NOT EXISTS summary text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS changes_category ON changes(organization_id,category,changed_at DESC);
