CREATE TABLE IF NOT EXISTS deployment_policies (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name text NOT NULL,
    description text,
    source_type text NOT NULL CHECK (source_type IN ('endpoint','repository','kubernetes')),
    discovery_depth text NOT NULL DEFAULT 'standard'
        CHECK (discovery_depth IN ('basic','standard','deep')),
    configuration jsonb NOT NULL DEFAULT '{}',
    expected_device_count integer CHECK (expected_device_count IS NULL OR expected_device_count >= 0),
    credential_ttl_seconds integer NOT NULL DEFAULT 86400
        CHECK (credential_ttl_seconds BETWEEN 60 AND 2592000),
    max_enrollments integer NOT NULL DEFAULT 100
        CHECK (max_enrollments BETWEEN 1 AND 100000),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id,id)
);
CREATE INDEX IF NOT EXISTS deployment_policies_query
    ON deployment_policies(organization_id,created_at DESC);

ALTER TABLE enrollment_codes
    ADD COLUMN IF NOT EXISTS id uuid DEFAULT gen_random_uuid(),
    ADD COLUMN IF NOT EXISTS policy_id uuid,
    ADD COLUMN IF NOT EXISTS max_uses integer,
    ADD COLUMN IF NOT EXISTS created_by text,
    ADD COLUMN IF NOT EXISTS revoked_by text;

UPDATE enrollment_codes SET id=gen_random_uuid() WHERE id IS NULL;
UPDATE enrollment_codes SET max_uses=uses_remaining WHERE max_uses IS NULL;
ALTER TABLE enrollment_codes ALTER COLUMN id SET NOT NULL;
ALTER TABLE enrollment_codes ALTER COLUMN max_uses SET NOT NULL;
ALTER TABLE enrollment_codes ALTER COLUMN max_uses SET DEFAULT 1;
ALTER TABLE enrollment_codes DROP CONSTRAINT IF EXISTS enrollment_codes_uses_remaining_check;
ALTER TABLE enrollment_codes ADD CONSTRAINT enrollment_codes_uses_remaining_check
    CHECK (uses_remaining >= 0);
ALTER TABLE enrollment_codes DROP CONSTRAINT IF EXISTS enrollment_codes_max_uses_check;
ALTER TABLE enrollment_codes ADD CONSTRAINT enrollment_codes_max_uses_check
    CHECK (max_uses >= 1);
CREATE UNIQUE INDEX IF NOT EXISTS enrollment_codes_org_id
    ON enrollment_codes(organization_id,id);
CREATE INDEX IF NOT EXISTS enrollment_codes_policy
    ON enrollment_codes(organization_id,policy_id,created_at DESC)
    WHERE policy_id IS NOT NULL;

ALTER TABLE enrollment_codes DROP CONSTRAINT IF EXISTS enrollment_codes_policy_fk;
ALTER TABLE enrollment_codes ADD CONSTRAINT enrollment_codes_policy_fk
    FOREIGN KEY (organization_id,policy_id)
    REFERENCES deployment_policies(organization_id,id) ON DELETE CASCADE;

CREATE TABLE IF NOT EXISTS deployment_policy_enrollments (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    policy_id uuid NOT NULL,
    credential_id uuid NOT NULL,
    target_id text NOT NULL,
    source_id text NOT NULL,
    source_type text NOT NULL CHECK (source_type IN ('endpoint','repository','kubernetes')),
    identity_fingerprint text NOT NULL,
    enrolled_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id,id),
    FOREIGN KEY (organization_id,policy_id)
        REFERENCES deployment_policies(organization_id,id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id,credential_id)
        REFERENCES enrollment_codes(organization_id,id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS deployment_policy_enrollments_policy
    ON deployment_policy_enrollments(organization_id,policy_id,enrolled_at DESC);
CREATE INDEX IF NOT EXISTS deployment_policy_enrollments_target
    ON deployment_policy_enrollments(organization_id,target_id,enrolled_at DESC);

ALTER TABLE discovery_targets
    ADD COLUMN IF NOT EXISTS deployment_policy_id uuid,
    ADD COLUMN IF NOT EXISTS deployment_enrollment_id uuid;
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS deployment_policy_id uuid,
    ADD COLUMN IF NOT EXISTS deployment_enrollment_id uuid;

ALTER TABLE deployment_policies ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS lens_tenant_isolation ON deployment_policies;
CREATE POLICY lens_tenant_isolation ON deployment_policies
    USING (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    )
    WITH CHECK (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    );

ALTER TABLE deployment_policy_enrollments ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS lens_tenant_isolation ON deployment_policy_enrollments;
CREATE POLICY lens_tenant_isolation ON deployment_policy_enrollments
    USING (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    )
    WITH CHECK (
        pg_has_role(current_user,'lens_worker','member') OR
        organization_id = NULLIF(current_setting('lens.organization_id',true),'')
    );
