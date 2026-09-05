# Managed self-serve deployment

Lens self-serve runs on the same Hub binary and PostgreSQL normalization path as a self-hosted deployment. It is enabled with feature flags so an Azure deployment can progress from internal testing to public signup without introducing billing or entitlement checks.

## Identity and Clerk

Set `LENS_AUTH_MODE=clerk`, `LENS_SELF_SERVE_ENABLED=true`, and configure:

- `LENS_CLERK_ISSUER`: the Clerk Frontend API issuer used for session JWT discovery and cached JWKS verification.
- `LENS_CLERK_PUBLISHABLE_KEY`: the browser-safe Clerk key.
- `LENS_CLERK_AUTHORIZED_PARTY`: the exact public Lens origin accepted in the JWT `azp` claim.
- `LENS_CLERK_WEBHOOK_SECRET`: the `whsec_...` secret for the Hub webhook at `/v1/auth/clerk/webhook`.

Enable Google, GitHub, and verified email/password in Clerk. Configure automatic account linking only for verified addresses, configure the Lens callback URLs, and leave MFA disabled for this MVP. Create the Clerk organization roles `org:owner`, `org:admin`, and `org:viewer`; Lens maps those roles to its owner, admin, and viewer permission sets. Enable organization creation and invitations. Disable Clerk's direct account-deletion control: Lens exposes `can_delete_account` from `/v1/session`, and a sole owner must transfer ownership or delete the Lens workspace before their identity is removed.

The browser creates or activates the first Clerk Organization and calls the idempotent workspace bootstrap API with its active-organization session token. Webhook delivery is not on this synchronous path. Invitation links for new and existing users remain Clerk-hosted flows; expired or revoked invitations are rejected by Clerk, while missing active organizations are recovered with the organization switcher.

## PostgreSQL roles and row security

Run migrations with the owner/admin connection in `LENS_DATABASE_URL`. Use separate login roles for HTTP requests and background workers in managed production:

```sql
CREATE ROLE lens_web_login LOGIN PASSWORD '<stored-in-key-vault>';
CREATE ROLE lens_worker_login LOGIN PASSWORD '<stored-in-key-vault>';
GRANT CONNECT ON DATABASE lens TO lens_web_login, lens_worker_login;
GRANT USAGE ON SCHEMA public TO lens_web_login, lens_worker_login;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO lens_web_login, lens_worker_login;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO lens_web_login, lens_worker_login;
GRANT lens_worker TO lens_worker_login;
```

Set `LENS_WEB_DATABASE_URL` to the web login and `LENS_WORKER_DATABASE_URL` to the worker login. The web server opens every authenticated request in a transaction and uses `SET LOCAL lens.organization_id`; row-security policies compare every tenant row to that value. The worker login is the only non-owner login that should be granted the `lens_worker` role. Do not grant it to the web login. The admin URL fallback is intended for development and migrations because a table owner bypasses normal PostgreSQL row security.

Apply equivalent default privileges for the migration owner before adding future tables. Rotate all three connection credentials through Azure Key Vault.

## Credential-free cloud trust

The Azure workload needs a user-assigned managed identity. Set its client ID and object ID in `LENS_AZURE_MANAGED_IDENTITY_CLIENT_ID` and `LENS_AZURE_MANAGED_IDENTITY_PRINCIPAL_ID`.

For AWS, configure a minimal Lens-owned broker role and set `LENS_AWS_BROKER_ROLE_ARN` and `LENS_AWS_FEDERATION_AUDIENCE`. The setup wizard creates a customer role with a connection-specific 128-bit external ID. Lens first federates its Azure identity into the broker and then assumes that customer role.

For Azure, create a multitenant Entra application with a federated credential trusting the managed identity. Set its client ID in `LENS_AZURE_APPLICATION_ID`; the default assertion audience is `api://AzureADTokenExchange`. The customer-side wizard creates the service principal and the generated read-only subscription role.

For GCP, set `LENS_GCP_WORKLOAD_ISSUER`, `LENS_GCP_WORKLOAD_AUDIENCE`, and `LENS_GCP_ASSERTION_AUDIENCE`. The generated Terraform creates Workload Identity Federation and viewer-only bindings; it never creates a service-account key.

Provider rollout flags are `LENS_AWS_CONNECTOR_ENABLED`, `LENS_AZURE_CONNECTOR_ENABLED`, and `LENS_GCP_CONNECTOR_ENABLED`. A disabled provider remains visible as unavailable in the environment catalog.

## Operations and launch gates

Cloud environments scan immediately after verification, daily with deterministic per-environment jitter, and on coalesced manual requests. PostgreSQL is the queue. Disconnect revokes the source, collector refresh credentials, queued jobs, and the current inventory projection immediately; retained observations purge after 90 days.

The first successful or partial scan creates a deferred `first_scan_completed` outbox event for owners. It is visible in-app; no delivery worker sends email until a transactional provider is intentionally selected.

Roll out in this order:

1. Clerk mode and self-serve enabled only in the internal Azure deployment.
2. AWS, Azure, and GCP live sandbox setup, revoke, reconnect, and teardown tests.
3. Invited beta with provider flags enabled selectively.
4. Unmetered public signup.

Monitor `product_events`, `cloud_scan_jobs`, and application metrics for signup conversion, verification reason, queue time, scan duration, partial/failure reason, queue depth, provider throttling, and cross-tenant authorization failures. Product events intentionally contain provider/kind/status timing only, never discovered names, evidence, prompts, content, or secret values.
