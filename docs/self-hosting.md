# Self-hosting Lens Hub

Production requires PostgreSQL, TLS at the ingress/proxy, a 32-byte or longer random JWT secret, and OIDC. Do not reuse the compose quickstart credentials.

Required environment variables are `LENS_DATABASE_URL`, `LENS_JWT_SECRET`, and `LENS_PUBLIC_URL`. Configure OIDC with `LENS_OIDC_ISSUER`, `LENS_OIDC_CLIENT_ID`, `LENS_OIDC_REDIRECT_URI`, and optionally `LENS_OIDC_CLIENT_SECRET` and `LENS_OIDC_ADMIN_GROUP`. Human access uses Authorization Code with PKCE. Keep `LENS_DEV_ADMIN_TOKEN` unset in production.

Multiple Hub replicas may run concurrently. Migrations are idempotent, ingestion/webhook/repository workers use PostgreSQL row locks, and no local disk is required. Back up PostgreSQL using your standard managed or self-hosted procedure.

Catalog enrichment is enabled by default. Set `LENS_CATALOG_ENABLED=false` for an air-gapped Hub or point `LENS_CATALOG_MANIFEST` at an internal OAK-compatible mirror. Discovery fails open when the catalog is unavailable.

The Helm chart expects an existing Secret containing `database-url`, `jwt-secret`, and optional `oidc-client-secret`. Organization enrollment secrets should remain in MDM, Kubernetes Secret, or other customer-controlled secret tooling.
