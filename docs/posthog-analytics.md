# Managed Lens activation analytics

Lens can send a deliberately small activation dataset to PostHog EU Cloud. It is available only when Clerk authentication and self-service onboarding are both enabled. It is off by default.

## Production gate

Do not enable capture until the privacy notice and legal basis have been approved. Create separate EU Cloud projects for staging and production, with different project tokens and pseudonym salts. In each project, disable IP retention, session replay, surveys, feature flags, error tracking, and autocapture.

Configure the Hub with:

```text
LENS_POSTHOG_ENABLED=true
LENS_POSTHOG_PROJECT_TOKEN=phc_...
LENS_POSTHOG_HOST=https://eu.i.posthog.com
LENS_POSTHOG_ID_SALT=<at least 32 random bytes>
LENS_DEPLOYMENT_ENVIRONMENT=staging|production
```

Keep the ID salt server-side and stable for the lifetime of a project. Changing it breaks longitudinal analysis and erasure lookups. Startup fails if analytics is enabled outside Clerk self-service mode, if the host is not HTTPS, or if required secrets are absent.

The browser token and ingestion host are returned at runtime from `/v1/auth/config`; they are not compiled into the UI image. The SDK is imported only after `/v1/session` returns an enabled pseudonymous user and workspace. `/install`, Clerk authentication screens, OIDC, development, and self-hosted deployments never initialize it.

## Data contract

Every event is allowlisted. Backend delivery rejects unknown events, properties, values, and free-form strings. Browser events pass through a final guard that removes URL, query, hash, referrer, DOM, customer, and unknown properties.

User IDs are `phu_` HMAC pseudonyms. Workspace IDs are independently domain-separated `phw_` HMAC pseudonyms. Raw Clerk IDs, names, email addresses, workspace names, inventory, evidence, entity IDs, commands, configuration, URLs, hostnames, repository names, tokens, and raw errors are never sent.

Common properties are `workspace_id` when applicable, `schema_version`, `origin`, `app_version`, and `deployment_environment`. Person profiles and GeoIP are disabled on each event. Actorless processing events use the workspace pseudonym as `distinct_id`; user actions use the user pseudonym. No PostHog group calls are made.

Credible discovery means a successfully normalized complete or partial scan containing at least one current root AI system. Zero-result successful scans emit `scan_completed` only. `first_credible_discovery_completed` and `first_credible_discovery_inspected` are each stored once per workspace.

## Delivery operations

New analytics-eligible `product_events` rows enter a durable queue in the same database transaction as the Lens action. Rows written before migration 0013 have no delivery state and are never backfilled.

Each Hub replica claims rows with `FOR UPDATE SKIP LOCKED` and a one-minute lease. The database event UUID is sent as the PostHog UUID. Analytics V1 acknowledgements mark delivery; failures use bounded exponential retry and dead-letter after ten attempts. Expired leases are reclaimable, and the stable UUID makes crash recovery idempotent.

The worker logs queue depth, oldest pending age, cumulative successes, retries, and dead letters every minute. Delivery errors are isolated from API responses, scan ingestion, and transaction outcomes. Alert on any dead letter or on an oldest pending age above 15 minutes.

Rollback is `LENS_POSTHOG_ENABLED=false` followed by a normal rollout. Lens remains available and new product events stay internal with no PostHog delivery state.

## Project insights

Build and validate these in staging with seeded workspaces before enabling production:

1. User funnel: `signup_completed` followed by `workspace_created`.
2. Workspace activation funnel: `workspace_created`, `connection_setup_started`, `environment_enrolled`, `scan_received`, `first_credible_discovery_completed`, and `first_credible_discovery_inspected`. Use HogQL and partition by `properties.workspace_id`; do not configure PostHog groups.
3. Activated workspace: first credible completion and first inspection within 30 days of workspace creation.
4. Median and P90 elapsed time from workspace creation to credible completion.
5. Setup conversion and abandonment broken down by `connection_type`; browser platform selection is available from `install_platform_selected`.
6. W1, W2, and W4 meaningful return among activated workspaces, using `inventory_viewed`, `system_opened`, `finding_opened`, `evidence_graph_viewed`, `changes_viewed`, and `export_generated`, not login or generic page views.

A base HogQL dataset for workspace milestones is:

```sql
SELECT
    properties.workspace_id AS workspace_id,
    minIf(timestamp, event = 'workspace_created') AS workspace_created_at,
    minIf(timestamp, event = 'connection_setup_started') AS setup_started_at,
    minIf(timestamp, event = 'environment_enrolled') AS enrolled_at,
    minIf(timestamp, event = 'scan_received') AS scan_received_at,
    minIf(timestamp, event = 'first_credible_discovery_completed') AS credible_at,
    minIf(timestamp, event = 'first_credible_discovery_inspected') AS inspected_at
FROM events
WHERE properties.schema_version = 1
  AND properties.deployment_environment = 'production'
  AND notEmpty(properties.workspace_id)
GROUP BY workspace_id
```

Use `dateDiff('second', workspace_created_at, credible_at)` from that dataset for time-to-value percentiles. Define activated workspaces by `credible_at >= workspace_created_at`, `inspected_at >= credible_at`, and `inspected_at <= workspace_created_at + INTERVAL 30 DAY`.

## Consent and erasure

Account Settings controls `PATCH /v1/session/analytics`. Opt-out prevents future browser capture and future actor-attributed backend delivery; actorless workspace processing milestones may continue. Global Privacy Control or Do Not Track disables browser capture for that browser without changing the account preference.

Opt-out is prospective. For an erasure request:

1. Obtain the authenticated Lens subject and current workspace ID through the normal support identity-verification process. Never place raw identifiers in a ticket sent to PostHog.
2. Run the Hub pseudonym derivation with the correct environment salt to obtain the `phu_` user ID and relevant `phw_` workspace IDs.
3. In the matching PostHog project, delete events for the user pseudonym. Delete workspace-pseudonym events only when the request and applicable policy cover the workspace dataset.
4. Record the request and completion in the approved privacy-operations system, then verify the pseudonyms no longer return events after PostHog's deletion process completes.
5. Do not rotate the global salt as an erasure mechanism; that would only make retained events harder to locate.

Only designated privacy administrators should have access to salts or PostHog deletion permissions.
