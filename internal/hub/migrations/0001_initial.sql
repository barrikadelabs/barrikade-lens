CREATE TABLE IF NOT EXISTS organizations (
    id text PRIMARY KEY,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sources (
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id text NOT NULL,
    source_type text NOT NULL CHECK (source_type IN ('endpoint','repository','kubernetes')),
    name text NOT NULL,
    platform text,
    collector_version text,
    last_sequence bigint NOT NULL DEFAULT 0,
    last_full_sequence bigint NOT NULL DEFAULT 0,
    last_seen_at timestamptz,
    last_full_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, id)
);

CREATE TABLE IF NOT EXISTS enrollment_codes (
    code_hash bytea PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    uses_remaining integer NOT NULL CHECK (uses_remaining > 0),
    source_type text NOT NULL DEFAULT 'endpoint',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS collector_refresh_tokens (
    token_hash bytea PRIMARY KEY,
    organization_id text NOT NULL,
    source_id text NOT NULL,
    scopes text[] NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, source_id) REFERENCES sources(organization_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ingestion_jobs (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL,
    source_id text NOT NULL,
    snapshot_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','processing','complete','failed')),
    payload jsonb,
    error_code text,
    error_message text,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    expires_at timestamptz,
    UNIQUE (organization_id, snapshot_id),
    FOREIGN KEY (organization_id, source_id) REFERENCES sources(organization_id, id) ON DELETE CASCADE
);
ALTER TABLE ingestion_jobs ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS ingestion_jobs_ready ON ingestion_jobs (next_attempt_at, created_at) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS entities (
    organization_id text NOT NULL,
    id text NOT NULL,
    kind text NOT NULL,
    canonical_key text,
    name text NOT NULL,
    attributes jsonb NOT NULL DEFAULT '{}',
    confidence text NOT NULL,
    provenance text[] NOT NULL DEFAULT '{}',
    current boolean NOT NULL DEFAULT true,
    stale boolean NOT NULL DEFAULT false,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    PRIMARY KEY (organization_id, id),
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS entities_query ON entities (organization_id, current, kind, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS entities_attributes_gin ON entities USING gin (attributes);

CREATE TABLE IF NOT EXISTS source_entities (
    organization_id text NOT NULL,
    source_id text NOT NULL,
    entity_id text NOT NULL,
    last_seen_at timestamptz NOT NULL,
    last_seen_sequence bigint NOT NULL,
    consecutive_full_misses integer NOT NULL DEFAULT 0,
    current boolean NOT NULL DEFAULT true,
    stale boolean NOT NULL DEFAULT false,
    PRIMARY KEY (organization_id, source_id, entity_id),
    FOREIGN KEY (organization_id, source_id) REFERENCES sources(organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, entity_id) REFERENCES entities(organization_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS relationships (
    organization_id text NOT NULL,
    id text NOT NULL,
    kind text NOT NULL,
    from_entity text NOT NULL,
    to_entity text NOT NULL,
    attributes jsonb NOT NULL DEFAULT '{}',
    confidence text NOT NULL,
    current boolean NOT NULL DEFAULT true,
    stale boolean NOT NULL DEFAULT false,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    PRIMARY KEY (organization_id, id),
    FOREIGN KEY (organization_id, from_entity) REFERENCES entities(organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, to_entity) REFERENCES entities(organization_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS relationships_query ON relationships (organization_id, current, kind, from_entity, to_entity);

CREATE TABLE IF NOT EXISTS source_relationships (
    organization_id text NOT NULL,
    source_id text NOT NULL,
    relationship_id text NOT NULL,
    last_seen_at timestamptz NOT NULL,
    last_seen_sequence bigint NOT NULL,
    consecutive_full_misses integer NOT NULL DEFAULT 0,
    current boolean NOT NULL DEFAULT true,
    stale boolean NOT NULL DEFAULT false,
    PRIMARY KEY (organization_id, source_id, relationship_id),
    FOREIGN KEY (organization_id, source_id) REFERENCES sources(organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, relationship_id) REFERENCES relationships(organization_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS evidence_observations (
    organization_id text NOT NULL,
    snapshot_id uuid NOT NULL,
    evidence_id text NOT NULL,
    source_id text NOT NULL,
    entity_ids text[] NOT NULL DEFAULT '{}',
    relationship_ids text[] NOT NULL DEFAULT '{}',
    detector_id text NOT NULL,
    detector_version text NOT NULL,
    method text NOT NULL,
    family text NOT NULL,
    specificity text NOT NULL,
    locator text,
    content_hash text,
    observed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '90 days'),
    PRIMARY KEY (organization_id, snapshot_id, evidence_id)
);
CREATE INDEX IF NOT EXISTS evidence_expiry ON evidence_observations (expires_at);

CREATE TABLE IF NOT EXISTS changes (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL,
    source_id text NOT NULL,
    entity_id text NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('entity.discovered','entity.updated','entity.stale','entity.removed')),
    changed_at timestamptz NOT NULL DEFAULT now(),
    snapshot_id uuid NOT NULL,
    details jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS changes_query ON changes (organization_id, changed_at DESC);

CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL,
    url text NOT NULL,
    signing_secret bytea NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webhook_outbox (
    id uuid PRIMARY KEY,
    webhook_id uuid NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    organization_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    last_status integer,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS webhook_outbox_pending ON webhook_outbox (next_attempt_at) WHERE delivered_at IS NULL;

CREATE TABLE IF NOT EXISTS catalog_sources (
    organization_id text NOT NULL,
    id text NOT NULL,
    provider_type text NOT NULL,
    display_name text NOT NULL,
    configuration jsonb NOT NULL,
    etag text,
    source_commit text,
    refreshed_at timestamptz,
    PRIMARY KEY (organization_id, id)
);

CREATE TABLE IF NOT EXISTS catalog_documents (
    organization_id text NOT NULL,
    source_id text NOT NULL,
    api_id text NOT NULL,
    document_type text NOT NULL,
    source_ref text NOT NULL,
    etag text,
    source_sha text,
    metadata jsonb NOT NULL,
    cached_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, source_id, api_id, document_type)
);

CREATE TABLE IF NOT EXISTS catalog_matches (
    organization_id text NOT NULL,
    entity_id text NOT NULL,
    source_id text NOT NULL,
    api_id text NOT NULL,
    confidence text NOT NULL,
    status text NOT NULL CHECK (status IN ('linked','suggested')),
    metadata jsonb NOT NULL DEFAULT '{}',
    matched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, entity_id, source_id, api_id),
    FOREIGN KEY (organization_id, entity_id) REFERENCES entities(organization_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS github_installations (
    installation_id bigint PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    account_login text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS github_webhook_deliveries (
    delivery_id text PRIMARY KEY,
    event_type text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS repository_scan_jobs (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL,
    installation_id bigint NOT NULL REFERENCES github_installations(installation_id) ON DELETE CASCADE,
    owner text NOT NULL,
    repository text NOT NULL,
    commit_sha text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','processing','complete','failed')),
    attempts integer NOT NULL DEFAULT 0,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (organization_id, owner, repository, commit_sha)
);
CREATE INDEX IF NOT EXISTS repository_scan_jobs_pending ON repository_scan_jobs(created_at) WHERE status='pending';

CREATE TABLE IF NOT EXISTS github_repositories (
    installation_id bigint NOT NULL REFERENCES github_installations(installation_id) ON DELETE CASCADE,
    organization_id text NOT NULL,
    owner text NOT NULL,
    repository text NOT NULL,
    source_id text NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, owner, repository),
    UNIQUE (organization_id, source_id),
    FOREIGN KEY (organization_id, source_id) REFERENCES sources(organization_id, id) ON DELETE CASCADE
);
