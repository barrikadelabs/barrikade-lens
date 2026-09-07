ALTER TABLE enrollment_codes
    ADD COLUMN IF NOT EXISTS environment_id uuid,
    ADD COLUMN IF NOT EXISTS revoked_at timestamptz;

ALTER TABLE enrollment_codes
    DROP CONSTRAINT IF EXISTS enrollment_codes_environment_fk;
ALTER TABLE enrollment_codes
    ADD CONSTRAINT enrollment_codes_environment_fk
    FOREIGN KEY (organization_id,environment_id)
    REFERENCES environment_connections(organization_id,id) ON DELETE CASCADE;

CREATE UNIQUE INDEX IF NOT EXISTS enrollment_codes_one_active_environment
    ON enrollment_codes(organization_id,environment_id)
    WHERE environment_id IS NOT NULL AND revoked_at IS NULL;

ALTER TABLE environment_connections
    ADD COLUMN IF NOT EXISTS first_result_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_result_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_result_status text;

ALTER TABLE environment_connections
    DROP CONSTRAINT IF EXISTS environment_connections_last_result_status_check;
ALTER TABLE environment_connections
    ADD CONSTRAINT environment_connections_last_result_status_check
    CHECK (last_result_status IS NULL OR last_result_status IN ('complete','partial','failed'));

CREATE TABLE IF NOT EXISTS endpoint_setup_handoffs (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    created_by text NOT NULL,
    expires_at timestamptz NOT NULL,
    last_viewed_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id,id),
    FOREIGN KEY (organization_id,environment_id)
        REFERENCES environment_connections(organization_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS endpoint_setup_handoffs_one_active
    ON endpoint_setup_handoffs(organization_id,environment_id)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS endpoint_setup_handoffs_expiry
    ON endpoint_setup_handoffs(expires_at)
    WHERE revoked_at IS NULL;

ALTER TABLE notification_outbox
    ADD COLUMN IF NOT EXISTS read_at timestamptz;
CREATE INDEX IF NOT EXISTS notification_outbox_unread
    ON notification_outbox(organization_id,created_at DESC)
    WHERE read_at IS NULL;

ALTER TABLE workspace_memberships
    ADD COLUMN IF NOT EXISTS provider_updated_at timestamptz;
ALTER TABLE managed_users
    ADD COLUMN IF NOT EXISTS provider_updated_at timestamptz;

CREATE TABLE IF NOT EXISTS clerk_organization_state (
    organization_id text PRIMARY KEY,
    status text NOT NULL CHECK (status IN ('active','deleted')),
    provider_updated_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE cloud_scan_jobs
    ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS heartbeat_at timestamptz;
CREATE INDEX IF NOT EXISTS cloud_scan_jobs_expired_lease
    ON cloud_scan_jobs(lease_expires_at)
    WHERE status IN ('running','ingesting');

ALTER TABLE endpoint_setup_handoffs ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS lens_tenant_isolation ON endpoint_setup_handoffs;
CREATE POLICY lens_tenant_isolation ON endpoint_setup_handoffs
    USING (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    )
    WITH CHECK (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    );

ALTER TABLE clerk_organization_state ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS lens_tenant_isolation ON clerk_organization_state;
CREATE POLICY lens_tenant_isolation ON clerk_organization_state
    USING (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    )
    WITH CHECK (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    );
