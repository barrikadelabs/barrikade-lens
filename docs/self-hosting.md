# Self-hosting Lens Hub

Production requires PostgreSQL, TLS at the ingress/proxy, a 32-byte or longer random JWT secret, and OIDC. Do not reuse the compose quickstart credentials.

Required environment variables are `LENS_DATABASE_URL`, `LENS_JWT_SECRET`, and `LENS_PUBLIC_URL`. Configure OIDC with `LENS_OIDC_ISSUER`, `LENS_OIDC_CLIENT_ID`, `LENS_OIDC_REDIRECT_URI`, and optionally `LENS_OIDC_CLIENT_SECRET` and `LENS_OIDC_ADMIN_GROUP`. Human access uses Authorization Code with PKCE. Keep `LENS_DEV_ADMIN_TOKEN` unset in production.

Multiple Hub replicas may run concurrently. The ordered migration runner verifies migration checksums and uses a PostgreSQL advisory lock, while ingestion/webhook/repository workers use row locks. Never edit an applied migration; add a new numbered migration. No local disk is required. Back up PostgreSQL using your standard managed or self-hosted procedure.

Catalog enrichment is enabled by default. Set `LENS_CATALOG_ENABLED=false` for an air-gapped Hub or point `LENS_CATALOG_MANIFEST` at an internal OAK-compatible mirror. Discovery fails open when the catalog is unavailable.

ARD declaration support is enabled by default but performs no network traffic until an administrator explicitly adds a catalog. Set `LENS_ARD_ENABLED=false` to disable the ARD APIs, UI, and catalog refresh worker. Remote catalogs must be credential-free HTTPS URLs without query strings or fragments. Loopback, private, link-local, cloud-metadata, unsafe redirect, and DNS-rebinding targets are blocked by default.

An internal catalog host can be enabled only with an exact deployment-level allowlist:

```sh
LENS_ARD_PRIVATE_HOST_ALLOWLIST=catalog.internal.example
```

The allowlist changes network reachability only; it does not permit URL credentials or relax parser limits. Catalog credentials are not supported or stored. Read [ARD declarations and discovery](ard.md) before enabling an internal source.

The Helm chart expects an existing Secret containing `database-url`, `jwt-secret`, and optional `oidc-client-secret`. Organization enrollment secrets should remain in MDM, Kubernetes Secret, or other customer-controlled secret tooling.

Coverage baselines are optional. Administrators can configure expected endpoint, repository, and cluster populations from the Coverage page or `PUT /v1/admin/coverage/baselines`. Leaving a value blank is materially different from zero: Lens reports the expected population as unknown and does not calculate a percentage.

Snapshot schema 1.2 adds catalog and declaration types. Hub accepts 1.1 and 1.2 during the rolling-upgrade window; catalog/declaration records require 1.2. A 1.0 snapshot receives HTTP 426 with an explicit collector-upgrade error. A legacy database migration creates one `legacy_identity` target for each existing source and never merges targets by hostname. For a pre-release local reset, stop Compose with volumes, rebuild, generate a new enrollment code, and re-enroll so the endpoint creates its persistent installation identity.
