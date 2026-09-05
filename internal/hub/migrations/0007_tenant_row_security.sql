-- The HTTP service sets lens.organization_id with SET LOCAL at the beginning of
-- every authenticated request transaction. A separate database login may be
-- granted the NOLOGIN lens_worker role for background processing.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='lens_worker') THEN
        CREATE ROLE lens_worker NOLOGIN;
    END IF;
END $$;

ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS lens_tenant_isolation ON organizations;
CREATE POLICY lens_tenant_isolation ON organizations
    USING (
        pg_has_role(current_user,'lens_worker','member') OR
        id = NULLIF(current_setting('lens.organization_id',true),'')
    )
    WITH CHECK (
        pg_has_role(current_user,'lens_worker','member') OR
        id = NULLIF(current_setting('lens.organization_id',true),'')
    );

DO $$
DECLARE
    tenant_table record;
BEGIN
    FOR tenant_table IN
        SELECT DISTINCT table_name
        FROM information_schema.columns
        WHERE table_schema='public'
          AND column_name='organization_id'
          AND table_name<>'organizations'
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', tenant_table.table_name);
        EXECUTE format('DROP POLICY IF EXISTS lens_tenant_isolation ON %I', tenant_table.table_name);
        EXECUTE format(
            'CREATE POLICY lens_tenant_isolation ON %I USING '
            || '(pg_has_role(current_user,''lens_worker'',''member'') OR organization_id = NULLIF(current_setting(''lens.organization_id'',true),'''')) '
            || 'WITH CHECK '
            || '(pg_has_role(current_user,''lens_worker'',''member'') OR organization_id = NULLIF(current_setting(''lens.organization_id'',true),''''))',
            tenant_table.table_name
        );
    END LOOP;
END $$;
