ALTER TABLE sources DROP CONSTRAINT IF EXISTS sources_source_type_check;
ALTER TABLE sources ADD CONSTRAINT sources_source_type_check CHECK (source_type IN ('endpoint','repository','kubernetes','catalog'));

ALTER TABLE discovery_targets DROP CONSTRAINT IF EXISTS discovery_targets_target_type_check;
ALTER TABLE discovery_targets ADD CONSTRAINT discovery_targets_target_type_check CHECK (target_type IN ('endpoint','repository','kubernetes','catalog'));

CREATE TABLE IF NOT EXISTS resource_catalog_configs (
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id uuid NOT NULL,
    source_id text NOT NULL,
    target_id text NOT NULL,
    name text NOT NULL,
    format text NOT NULL CHECK (format IN ('ard')),
    url text NOT NULL,
    refresh_interval_seconds integer NOT NULL DEFAULT 21600 CHECK (refresh_interval_seconds BETWEEN 3600 AND 86400),
    nested_policy text NOT NULL DEFAULT 'same_site' CHECK (nested_policy IN ('same_site','disabled')),
    etag text,
    last_modified text,
    last_content_hash text,
    last_attempt_at timestamptz,
    last_success_at timestamptz,
    next_refresh_at timestamptz NOT NULL DEFAULT now(),
    last_error_code text,
    last_error_message text,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,id),
    UNIQUE (organization_id,url),
    FOREIGN KEY (organization_id,source_id) REFERENCES sources(organization_id,id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id,target_id) REFERENCES discovery_targets(organization_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS resource_catalog_configs_due ON resource_catalog_configs(next_refresh_at) WHERE enabled;
CREATE INDEX IF NOT EXISTS resource_catalog_configs_org ON resource_catalog_configs(organization_id,created_at DESC);

CREATE TABLE IF NOT EXISTS resource_declarations (
    organization_id text NOT NULL,
    entity_id text NOT NULL,
    identifier text NOT NULL,
    publisher_domain text NOT NULL,
    media_type text NOT NULL,
    mapped_kind text NOT NULL,
    delivery text NOT NULL CHECK (delivery IN ('url','inline')),
    artifact_url text,
    descriptor_host text,
    trust_identity_alignment text NOT NULL CHECK (trust_identity_alignment IN ('absent','aligned','misaligned','unresolved')),
    signature_status text NOT NULL CHECK (signature_status IN ('absent','present_unverified','malformed','unsupported')),
    source_ids text[] NOT NULL DEFAULT '{}',
    catalog_ids text[] NOT NULL DEFAULT '{}',
    alignment_status text NOT NULL DEFAULT 'declared_only' CHECK (alignment_status IN ('matched','declared_only','conflict')),
    matched_entity_id text,
    material_digest text NOT NULL,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    current boolean NOT NULL DEFAULT true,
    PRIMARY KEY (organization_id,entity_id),
    FOREIGN KEY (organization_id,entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS resource_declarations_query ON resource_declarations(organization_id,current,alignment_status,last_seen_at DESC,entity_id);
CREATE INDEX IF NOT EXISTS resource_declarations_publisher ON resource_declarations(organization_id,publisher_domain,media_type,mapped_kind);

CREATE TABLE IF NOT EXISTS resource_matches (
    organization_id text NOT NULL,
    declaration_entity_id text NOT NULL,
    observed_entity_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('linked','suggested','conflict')),
    confidence text NOT NULL CHECK (confidence IN ('confirmed','likely','possible')),
    reason text NOT NULL,
    matched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,declaration_entity_id,observed_entity_id),
    FOREIGN KEY (organization_id,declaration_entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id,observed_entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS resource_matches_observed ON resource_matches(organization_id,observed_entity_id,status);
CREATE INDEX IF NOT EXISTS entities_ard_name_candidates
    ON entities(organization_id,kind,lower(btrim(name)),id)
    WHERE current AND kind IN ('agent','mcp_server','skill','api_service','workflow');
