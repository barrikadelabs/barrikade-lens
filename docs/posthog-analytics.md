# Managed Lens product analytics

Lens can send a privacy-minimized product intelligence dataset to PostHog EU Cloud. It covers activation, engagement, reliability, performance, surveys, feature-flag exposure, and maximally masked session replay. It is available only when Clerk authentication and self-service onboarding are both enabled. It is off by default.

## Production gate

Do not enable capture until the privacy notice and legal basis have been approved. PostHog's free plan permits one project, so the pilot and initial production rollout may share the existing EU project and must be separated with `deployment_environment`; use a different pseudonym salt in each deployment. If the account later supports multiple projects, split staging and production and use different tokens too. Disable IP retention and autocapture in the project. Enable session replay, surveys, feature flags, error tracking, and web performance; the Lens SDK applies stricter local privacy controls than the project defaults.

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

Every product event is allowlisted. Backend delivery rejects unknown events, properties, values, and free-form strings. Browser events pass through a final guard that removes URL, query, hash, referrer, DOM, customer, and unknown properties. The guard preserves only the browser-safe project token, pseudonymous identity/session fields, bounded device/runtime facts, and the event-specific contract.

User IDs are `phu_` HMAC pseudonyms. Workspace IDs are independently domain-separated `phw_` HMAC pseudonyms. Raw Clerk IDs, names, email addresses, workspace names, inventory, evidence, entity IDs, commands, configuration, URLs, hostnames, repository names, tokens, and raw errors are never sent.

Common properties are `workspace_id` when applicable, `schema_version`, `origin`, `app_version`, and `deployment_environment`. Person profiles and GeoIP are disabled on each event. Actorless processing events use the workspace pseudonym as `distinct_id`; user actions use the user pseudonym. No PostHog group calls are made.

Browser schema version 2 adds:

- `lens_interaction` with bounded `surface`, `interaction`, and optional semantic control, connection, platform, or export-format properties. It measures filters, searches (presence only, never the query), pagination, refreshes, setup progress, command copying, handoffs, verification, manual scans, coverage baselines, notifications, navigation, and exports.
- `$web_vitals` with numeric LCP, CLS, FCP, and INP values only. Attribution objects and navigation URLs are removed.
- `$exception` with a bounded JavaScript error class and the constant message `Lens UI exception`. Original messages, stack traces, filenames, source URLs, and breadcrumbs are removed.
- Survey display/completion metadata and numeric rating answers. Free-text and multiple-choice strings are removed, so production surveys should use numeric rating or NPS questions when the answer itself is required for analysis.
- `$feature_flag_called` with a bounded flag key and boolean, numeric, or slug-like variant. Feature flags must not encode customer or workspace information in their key or variant.

Session replay is enabled only after the authenticated analytics session exists. All text and inputs are masked, all string-valued element attributes are masked, and image, video, audio, iframe, canvas, and SVG subtrees are blocked. Network URLs, request/response bodies, headers, console logs, fonts, cross-origin iframes, canvas frames, and JSON-LD are not recorded. PostHog documents that these browser-side masks run before data is transmitted. Replay is stopped and identity reset on opt-out, logout, or workspace change.

Autocapture, automatic page/pageleave capture, heatmaps, rage/dead clicks, console capture, and person profiles remain disabled. Those features depend on DOM-derived selectors, text, attributes, URLs, or uncontrolled logs and are incompatible with Lens's permanent data boundary. Semantic `lens_interaction` events provide the actionable equivalent without collecting customer content.

## Free-plan operating envelope

PostHog currently includes monthly free allowances for product events, session recordings, feature-flag requests/experiments, exceptions, surveys, warehouse rows, data-pipeline events, PostHog AI, Inbox, workflows, logs, and Replay Vision. The authoritative limits are on [PostHog's pricing page](https://posthog.com/pricing). A no-card free account stops ingestion at its allowance rather than charging overage. If a card is ever added, set each product's billing limit to zero before enabling it.

Lens directly uses product analytics, replay, flags/experiments, sanitized error tracking, surveys, and PostHog AI over that collected dataset. Warehouse sources, data pipelines, workflows, Inbox, Replay Vision, and PostHog Desktop are configured in PostHog rather than in the Lens runtime. Browser/backend logs and AI observability are not enabled because Lens console or model payloads can contain data outside this contract.

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
7. Interaction trend: `lens_interaction`, broken down by `surface`, `interaction`, and `control`, with a weekly unique-user view.
8. Friction: sanitized `$exception` count and affected sessions, Core Web Vitals distributions, and masked replays linked to affected pseudonymous sessions.
9. Feature adoption: `$feature_flag_called` exposure followed by the intended semantic success event. Use experiments only after declaring a primary success metric and guardrail.
10. Feedback: rating-only survey response distribution, activation state at response time, and response rate from shown to sent.

## Existing EU project checklist

Use the single existing Barrikade EU project; do not create another project on the free plan.

1. Keep **Autocapture**, **Heatmaps**, **Record network bodies**, **Record console logs**, and IP/GeoIP enrichment off in project settings.
2. Turn on **Session replay**, but leave all masking controls at their strictest setting. Lens also enforces stricter masks in code, so a project-setting regression cannot reveal text, inputs, attributes, media, network metadata, or canvas frames.
3. Turn on **Error tracking**. Do not upload source maps in this phase. Captured occurrences retain sanitized function/line coordinates but never the original exception message or source URL.
4. Turn on **Surveys**. Use rating/NPS questions for analyzable answers; written answers and string choices are intentionally removed by the client guard.
5. Create flags with keys and variants containing only letters, digits, `_`, or `-`. Use flags for reversible UI releases and experiments, never authorization, data access, or backend safety decisions.
6. Create a dashboard called `Lens · Product health` containing the ten insights above. Pin activation, time-to-value, W1/W2/W4 return, setup abandonment, interaction adoption, exceptions, Web Vitals, and survey response rate.
7. Configure PostHog alerts for a material activation drop, a sustained exception increase, and P90 LCP/INP regression. Alerts should link to the masked replay cohort and never include raw event payloads in third-party messages.
8. Add short product context to PostHog AI/Inbox: credible discovery is the activation milestone; meaningful use is investigation or export; login alone is not retention. Review suggested insights freely, but keep automatic paid PR creation disabled.

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
