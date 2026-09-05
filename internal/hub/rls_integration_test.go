package hub

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestTenantRowSecurityRejectsCrossOrganizationReadsAndWrites(t *testing.T) {
	ctx, pool := integrationPool(t)
	orgA := "rls-a-" + uuid.NewString()
	orgB := "rls-b-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'RLS A'),($2,'RLS B')`, orgA, orgB); err != nil {
		t.Fatal(err)
	}
	environmentB := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO environment_connections(id,organization_id,kind,provider,display_name,created_by) VALUES($1,$2,'endpoint','endpoint','Other tenant endpoint','test')`, environmentB, orgB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id IN ($1,$2)`, orgA, orgB)
	})

	var tenantTables, protectedTables int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT c.table_name),count(DISTINCT c.table_name) FILTER(WHERE t.relrowsecurity)
		FROM information_schema.columns c JOIN pg_class t ON t.relname=c.table_name JOIN pg_namespace n ON n.oid=t.relnamespace AND n.nspname=c.table_schema
		WHERE c.table_schema='public' AND c.column_name='organization_id'`).Scan(&tenantTables, &protectedTables); err != nil {
		t.Fatal(err)
	}
	if tenantTables == 0 || protectedTables != tenantTables {
		t.Fatalf("tenant RLS coverage is incomplete: %d of %d tables", protectedTables, tenantTables)
	}

	role := "lens_rls_test_" + uuid.NewString()
	quotedRole := pgx.Identifier{role}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+quotedRole+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP ROLE IF EXISTS "+quotedRole) })
	if _, err := pool.Exec(ctx, "GRANT USAGE ON SCHEMA public TO "+quotedRole); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "GRANT SELECT,INSERT ON organizations,environment_connections TO "+quotedRole); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+quotedRole); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('lens.organization_id',$1,true)`, orgA); err != nil {
		t.Fatal(err)
	}
	var ownOrganizations, foreignOrganizations, foreignEnvironments int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE id=$1`, orgA).Scan(&ownOrganizations); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE id=$1`, orgB).Scan(&foreignOrganizations); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM environment_connections WHERE organization_id=$1`, orgB).Scan(&foreignEnvironments); err != nil {
		t.Fatal(err)
	}
	if ownOrganizations != 1 || foreignOrganizations != 0 || foreignEnvironments != 0 {
		t.Fatalf("unexpected RLS visibility: own=%d foreign_org=%d foreign_environment=%d", ownOrganizations, foreignOrganizations, foreignEnvironments)
	}

	_, err = tx.Exec(ctx, `INSERT INTO environment_connections(id,organization_id,kind,provider,display_name,created_by) VALUES($1,$2,'endpoint','endpoint','Cross-tenant write','test')`, uuid.New(), orgB)
	if err == nil {
		t.Fatal("cross-tenant insert unexpectedly passed row security")
	}
	if got := err.Error(); got == "" {
		t.Fatal(fmt.Errorf("cross-tenant insert returned an empty error"))
	}
}
