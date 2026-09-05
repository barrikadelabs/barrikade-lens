ALTER TABLE sources DROP CONSTRAINT IF EXISTS sources_source_type_check;
ALTER TABLE sources ADD CONSTRAINT sources_source_type_check
    CHECK (source_type IN ('endpoint','repository','kubernetes','cloud'));

ALTER TABLE discovery_targets DROP CONSTRAINT IF EXISTS discovery_targets_target_type_check;
ALTER TABLE discovery_targets ADD CONSTRAINT discovery_targets_target_type_check
    CHECK (target_type IN ('endpoint','repository','kubernetes','cloud'));

ALTER TABLE coverage_baselines DROP CONSTRAINT IF EXISTS coverage_baselines_target_type_check;
ALTER TABLE coverage_baselines ADD CONSTRAINT coverage_baselines_target_type_check
    CHECK (target_type IN ('endpoint','repository','kubernetes','cloud'));

CREATE TABLE IF NOT EXISTS workspace_memberships (
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('owner','admin','viewer')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked','deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,user_id)
);
CREATE INDEX IF NOT EXISTS workspace_memberships_user
    ON workspace_memberships(user_id,organization_id) WHERE status='active';

CREATE TABLE IF NOT EXISTS environment_connections (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('aws_account','azure_subscription','gcp_project','endpoint','github_repository','kubernetes_cluster')),
    provider text CHECK (provider IS NULL OR provider IN ('aws','azure','gcp','endpoint','github','kubernetes')),
    external_id text,
    display_name text NOT NULL,
    connection_status text NOT NULL DEFAULT 'setup_pending'
        CHECK (connection_status IN ('setup_pending','verifying','connected','auth_error','disconnected')),
    configuration jsonb NOT NULL DEFAULT '{}',
    target_id text,
    source_id text,
    schedule_enabled boolean NOT NULL DEFAULT true,
    schedule_cadence text NOT NULL DEFAULT 'daily' CHECK (schedule_cadence='daily'),
    next_scan_at timestamptz,
    verified_at timestamptz,
    disconnected_at timestamptz,
    purge_after timestamptz,
    last_error_code text,
    last_error_message text,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id,id),
    FOREIGN KEY (organization_id,target_id) REFERENCES discovery_targets(organization_id,id),
    FOREIGN KEY (organization_id,source_id) REFERENCES sources(organization_id,id)
);
CREATE UNIQUE INDEX IF NOT EXISTS environment_connections_external_active
    ON environment_connections(organization_id,kind,external_id)
    WHERE external_id IS NOT NULL AND connection_status <> 'disconnected';
CREATE INDEX IF NOT EXISTS environment_connections_schedule
    ON environment_connections(next_scan_at)
    WHERE connection_status='connected' AND schedule_enabled=true;

-- Existing enrolled targets become first-class environments without changing
-- their source or target identity. Their collectors own their schedules.
INSERT INTO environment_connections(
    id,organization_id,kind,provider,external_id,display_name,connection_status,
    target_id,source_id,schedule_enabled,verified_at,created_by,created_at,updated_at
)
SELECT gen_random_uuid(),t.organization_id,
       CASE t.target_type
           WHEN 'repository' THEN 'github_repository'
           WHEN 'kubernetes' THEN 'kubernetes_cluster'
           ELSE 'endpoint'
       END,
       CASE t.target_type
           WHEN 'repository' THEN 'github'
           WHEN 'kubernetes' THEN 'kubernetes'
           ELSE 'endpoint'
       END,
       t.id,t.name,'connected',t.id,
       (SELECT min(s.id) FROM sources s
        WHERE s.organization_id=t.organization_id AND s.target_id=t.id AND s.revoked_at IS NULL),
       false,COALESCE(t.last_seen_at,t.created_at),'migration',t.created_at,now()
FROM discovery_targets t
WHERE NOT EXISTS (
    SELECT 1 FROM environment_connections e
    WHERE e.organization_id=t.organization_id AND e.target_id=t.id
);

CREATE TABLE IF NOT EXISTS connector_setup_sessions (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    kind text NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','consumed','expired','cancelled')),
    setup_payload jsonb NOT NULL DEFAULT '{}',
    created_by text NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id,id),
    FOREIGN KEY (organization_id,environment_id) REFERENCES environment_connections(organization_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS connector_setup_sessions_pending
    ON connector_setup_sessions(organization_id,environment_id,expires_at)
    WHERE state='pending';

CREATE TABLE IF NOT EXISTS cloud_scan_jobs (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL,
    trigger text NOT NULL CHECK (trigger IN ('first_scan','manual','scheduled')),
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','ingesting','complete','partial','failed','cancelled')),
    phase text NOT NULL DEFAULT 'queued',
    progress jsonb NOT NULL DEFAULT '{}',
    requested_by text,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ingestion_job_id uuid,
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    UNIQUE (organization_id,id),
    FOREIGN KEY (organization_id,environment_id) REFERENCES environment_connections(organization_id,id) ON DELETE CASCADE,
    FOREIGN KEY (ingestion_job_id) REFERENCES ingestion_jobs(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS cloud_scan_jobs_one_active
    ON cloud_scan_jobs(environment_id)
    WHERE status IN ('queued','running','ingesting');
CREATE INDEX IF NOT EXISTS cloud_scan_jobs_ready
    ON cloud_scan_jobs(next_attempt_at,created_at)
    WHERE status='queued';

CREATE TABLE IF NOT EXISTS workspace_audit_events (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    event_type text NOT NULL,
    target_type text,
    target_id text,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS workspace_audit_events_query
    ON workspace_audit_events(organization_id,created_at DESC);

CREATE TABLE IF NOT EXISTS product_events (
    id uuid PRIMARY KEY,
    organization_id text REFERENCES organizations(id) ON DELETE CASCADE,
    actor_id text,
    event_type text NOT NULL,
    properties jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS product_events_query
    ON product_events(organization_id,event_type,created_at DESC);

CREATE TABLE IF NOT EXISTS notification_outbox (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    recipient_role text NOT NULL DEFAULT 'owner',
    payload jsonb NOT NULL DEFAULT '{}',
    delivery_status text NOT NULL DEFAULT 'deferred'
        CHECK (delivery_status IN ('deferred','pending','delivered','failed')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS notification_first_scan_once
    ON notification_outbox(organization_id,(payload->>'environment_id'),event_type)
    WHERE event_type='first_scan_completed';

CREATE TABLE IF NOT EXISTS clerk_webhook_deliveries (
    event_id text PRIMARY KEY,
    event_type text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
