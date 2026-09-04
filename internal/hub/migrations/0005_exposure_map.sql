CREATE TABLE IF NOT EXISTS entity_context (
    organization_id text NOT NULL,
    entity_id text NOT NULL,
    owner_name text,
    owner_type text CHECK (owner_type IS NULL OR owner_type IN ('person','team')),
    environment text CHECK (environment IS NULL OR environment IN ('development','test','staging','production')),
    criticality text CHECK (criticality IS NULL OR criticality IN ('low','medium','high','critical')),
    sensitivity text CHECK (sensitivity IS NULL OR sensitivity IN ('public','internal','confidential','restricted')),
    data_categories text[] NOT NULL DEFAULT '{}',
    trust_boundary text CHECK (trust_boundary IS NULL OR trust_boundary IN ('internal','partner','third_party')),
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,entity_id),
    FOREIGN KEY (organization_id,entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE,
    CHECK (data_categories <@ ARRAY['personal','health','payment','financial','credentials','source_code','customer']::text[])
);

CREATE TABLE IF NOT EXISTS entity_context_history (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL,
    entity_id text NOT NULL,
    context jsonb NOT NULL,
    changed_by text NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id,entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS entity_context_history_lookup ON entity_context_history(organization_id,entity_id,changed_at DESC);

CREATE TABLE IF NOT EXISTS catalog_link_overrides (
    organization_id text NOT NULL,
    entity_id text NOT NULL,
    source_id text NOT NULL,
    api_id text NOT NULL,
    entry_reference text NOT NULL,
    selected_by text NOT NULL,
    selected_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,entity_id,source_id),
    FOREIGN KEY (organization_id,entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS catalog_index_entries (
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    source_id text NOT NULL,
    entry_id text NOT NULL,
    provider_id text NOT NULL,
    api_family text,
    api_version text,
    display_name text NOT NULL,
    entry_reference text NOT NULL,
    refreshed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,source_id,entry_id)
);
CREATE INDEX IF NOT EXISTS catalog_index_entries_search ON catalog_index_entries(organization_id,source_id,provider_id,api_family,api_version);

CREATE TABLE IF NOT EXISTS catalog_api_operations (
    organization_id text NOT NULL,
    source_id text NOT NULL,
    api_id text NOT NULL,
    operation_key text NOT NULL,
    operation_id text NOT NULL,
    method text NOT NULL,
    path text NOT NULL,
    summary text,
    tags text[] NOT NULL DEFAULT '{}',
    capability_class text NOT NULL CHECK (capability_class IN ('read','state_changing_potential','destructive_potential')),
    auth_scheme_types text[] NOT NULL DEFAULT '{}',
    auth_scopes text[] NOT NULL DEFAULT '{}',
    cached_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,source_id,api_id,operation_key)
);
CREATE INDEX IF NOT EXISTS catalog_api_operations_class ON catalog_api_operations(organization_id,source_id,api_id,capability_class);

CREATE TABLE IF NOT EXISTS exposure_findings (
    organization_id text NOT NULL,
    id text NOT NULL,
    root_entity_id text NOT NULL,
    destination_entity_id text,
    rule_id text NOT NULL,
    rule_version text NOT NULL,
    severity text NOT NULL CHECK (severity IN ('critical','high','medium','low')),
    title text NOT NULL,
    explanation text NOT NULL,
    recommended_next_step text NOT NULL,
    path jsonb NOT NULL DEFAULT '[]',
    evidence_bases text[] NOT NULL DEFAULT '{}',
    current boolean NOT NULL DEFAULT true,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    PRIMARY KEY (organization_id,id),
    FOREIGN KEY (organization_id,root_entity_id) REFERENCES entities(organization_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS exposure_findings_page ON exposure_findings(organization_id,current,severity,last_seen_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS exposure_findings_root ON exposure_findings(organization_id,root_entity_id,current,severity);

CREATE TABLE IF NOT EXISTS exposure_finding_history (
    id text NOT NULL,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    finding_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('current','resolved')),
    finding jsonb NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,id)
);
CREATE INDEX IF NOT EXISTS exposure_finding_history_lookup ON exposure_finding_history(organization_id,finding_id,recorded_at DESC);

CREATE TABLE IF NOT EXISTS exposure_evaluation_jobs (
    organization_id text PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Catalogue operations are a derived capability projection, not observed graph
-- entities. Preserve operations found directly by repository collectors.
UPDATE entities
SET current=false,stale=true,last_seen_at=now()
WHERE kind IN ('api_operation','workflow')
  AND attributes->>'catalog_enriched'='true';
