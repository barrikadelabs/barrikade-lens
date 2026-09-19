# Million-entity scale gate

The weekly `scale` workflow protects Lens's two-second inventory target with one million current systems. It also times the graph investigation paths used by the product: seek pagination, one-hop relationship expansion, recent material changes, and evidence lookup.

The fixture is deliberately not a flat best case. It contains two organizations, four target/source combinations in the organization under test, a stale Kubernetes target, 20,000 stale source observations, 100,300 current relationships, a 300-edge selected-system fan-out, 50,000 changes, and 50,200 evidence observations. A foreign-tenant canary is present in every measured path. The test fails if the primary organization can read it.

## Reproduce locally

Start the same PostgreSQL major version used by CI:

```sh
docker run --rm -d --name lens-scale-postgres \
  -e POSTGRES_DB=lens \
  -e POSTGRES_USER=lens \
  -e POSTGRES_PASSWORD=lens \
  -p 55435:5432 postgres:18-alpine
```

Wait for readiness, then run the scale and RLS gates:

```sh
until docker exec lens-scale-postgres pg_isready -U lens >/dev/null 2>&1; do sleep 1; done
mkdir -p .artifacts/scale
LENS_TEST_DATABASE_URL='postgres://lens:lens@127.0.0.1:55435/lens?sslmode=disable' \
LENS_RUN_SCALE_TESTS=1 \
LENS_SCALE_ARTIFACT_DIR=.artifacts/scale \
go test -v \
  -run '^(TestMillionEntityInventoryQueryUnderTwoSeconds|TestTenantRowSecurityRejectsCrossOrganizationReadsAndWrites)$' \
  -count=1 -timeout=20m ./internal/hub
```

Stop the disposable database when finished:

```sh
docker stop lens-scale-postgres
```

Fixture creation is excluded from the query timings. Each path runs twice after resetting application database connections and three times on warm connections. The first scheduled run also starts with a fresh PostgreSQL service and a newly loaded dataset; a connection reset does not claim to evict PostgreSQL or operating-system page caches.

Local runs explicitly delete the fixture so a shared development database is not polluted. CI sets `LENS_SCALE_DISPOSABLE_DATABASE=1` because its PostgreSQL service container is destroyed with the job; skipping redundant million-row deletion there does not change fixture creation, measurement, or diagnostics.

The inventory page and its next cursor page must each finish in under two seconds and return exactly 100 bounded results. The graph paths use the same ceiling so regressions fail early rather than becoming an untracked benchmark trend. The test verifies the full one-million-current-entity cardinality before timing anything.

## Diagnose a failure

Download the `million-entity-diagnostics-*` artifact from the workflow run. It is uploaded even when the test fails and contains:

- `test.log` with fixture and request timings;
- `measurements.json` with cold-connection and warm measurements plus response sizes;
- `cardinality.txt` proving the dataset was not weakened;
- `postgres-statistics.txt` with live/dead tuple and analyze statistics;
- `inventory-page-plan.txt`, `one-hop-plan.txt`, `recent-changes-plan.txt`, and `evidence-lookup-plan.txt`, each captured with `EXPLAIN (ANALYZE, BUFFERS, VERBOSE)`.

Healthy plans use `entity_posture_system_page` for inventory seek pagination, a `BitmapOr` over `relationships_current_from_page` and `relationships_current_to_page` for one-hop expansion, `changes_recent_page` for recent history, and `evidence_observations_entity_ids` for evidence membership.

Compare actual versus estimated rows, index searches, heap fetches, sort memory, and shared reads/hits. If cardinality estimates drift, rerun `ANALYZE` before changing indexes. Do not raise the two-second threshold or reduce the million current entities. Preserve organization predicates in every query and run the RLS gate with any query-shape change.

## Why the gate stopped running

The September 2026 failures occurred during fixture setup, before a timed query: after the pgx upgrade, parameterized `Exec` calls containing multiple SQL commands were prepared, and PostgreSQL rejected them with `cannot insert multiple commands into a prepared statement`. The repaired fixture issues one statement per call. Running past that setup failure also exposed that the old test mixed unrelated overview aggregation into a test named for inventory; the restored gate reports each required query independently, so a future artifact identifies the exact regressed path.
