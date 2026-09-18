ALTER TABLE github_installations
    ADD COLUMN IF NOT EXISTS environment_id uuid REFERENCES environment_connections(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS repository_selection text,
    ADD COLUMN IF NOT EXISTS selected_repository_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS revoked_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_error_code text,
    ADD COLUMN IF NOT EXISTS last_error_message text,
    ADD COLUMN IF NOT EXISTS first_scan_started_at timestamptz,
    ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS github_installations_environment
    ON github_installations(organization_id, environment_id);

CREATE TABLE IF NOT EXISTS github_repository_selections (
    installation_id bigint NOT NULL REFERENCES github_installations(installation_id) ON DELETE CASCADE,
    organization_id text NOT NULL,
    owner text NOT NULL,
    repository text NOT NULL,
    selected_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, owner, repository)
);

ALTER TABLE github_repository_selections ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS lens_tenant_isolation ON github_repository_selections;
CREATE POLICY lens_tenant_isolation ON github_repository_selections
    USING (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    )
    WITH CHECK (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    );
