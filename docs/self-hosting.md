# Self-hosting Lens Hub

Production requires PostgreSQL, TLS at the ingress/proxy, a 32-byte or longer random JWT secret, and OIDC. Do not reuse the compose quickstart credentials.

Required environment variables are `LENS_DATABASE_URL`, `LENS_JWT_SECRET`, and `LENS_PUBLIC_URL`. Configure OIDC with `LENS_OIDC_ISSUER`, `LENS_OIDC_CLIENT_ID`, `LENS_OIDC_REDIRECT_URI`, and optionally `LENS_OIDC_CLIENT_SECRET` and `LENS_OIDC_ADMIN_GROUP`. Human access uses Authorization Code with PKCE. Keep `LENS_DEV_ADMIN_TOKEN` unset in production.

Multiple Hub replicas may run concurrently. The ordered migration runner verifies migration checksums and uses a PostgreSQL advisory lock, while ingestion/webhook/repository workers use row locks. Never edit an applied migration; add a new numbered migration. No local disk is required. Back up PostgreSQL using your standard managed or self-hosted procedure.

Catalog enrichment is enabled by default. Set `LENS_CATALOG_ENABLED=false` for an air-gapped Hub or point `LENS_CATALOG_MANIFEST` at an internal OAK-compatible mirror. Discovery fails open when the catalog is unavailable.

The Helm chart expects an existing Secret containing `database-url`, `jwt-secret`, and optional `oidc-client-secret`. Organization enrollment secrets should remain in MDM, Kubernetes Secret, or other customer-controlled secret tooling.

Coverage baselines are optional. Administrators can configure expected endpoint, repository, and cluster populations from the Coverage page or `PUT /v1/admin/coverage/baselines`. Leaving a value blank is materially different from zero: Lens reports the expected population as unknown and does not calculate a percentage.

Snapshot schema 1.1 is a breaking collector upgrade. A 1.0 snapshot receives HTTP 426 with an explicit re-enrollment/upgrade error. A legacy database migration creates one `legacy_identity` target for each existing source and never merges targets by hostname. For a pre-release local reset, stop Compose with volumes, rebuild, generate a new enrollment code, and re-enroll so the endpoint creates its persistent installation identity.
