# Endpoint beta operations

The endpoint beta is an open-signup release for CISOs who need a useful first result without a deployment project. Endpoint collection is the only enabled connector. The four primary product sections are Overview, Findings, Inventory, and Connections.

## Clerk tenant

Use a dedicated production Clerk instance and configure:

- verified email and password plus Google as the only sign-up methods;
- verified-email matching before Google account linking;
- GitHub social sign-in, unverified account linking, and MFA disabled for this beta;
- direct account deletion disabled in Clerk, because Lens enforces sole-owner transfer or workspace deletion in Account Settings;
- organization creation enabled, with an explicit organization name required by Lens;
- lifecycle webhooks for organization, membership, role, removal, and user deletion events.

Set `LENS_AUTH_MODE=clerk`, the Clerk issuer, publishable key, backend secret key, authorized party, and webhook secret. The backend secret is required because Lens applies the sole-owner rule before deleting the Clerk identity; keep direct deletion disabled in Clerk's own account UI. A new organization bootstrap is idempotent and assigns its creator the Lens `owner` role. Clerk proves identity; the Lens membership row controls application authorization after bootstrap.

Exercise a fresh email/password sign-up and a fresh Google sign-up against the production configuration before opening registration. Repeat the acceptance after changes to Clerk domains, session templates, social providers, or webhook delivery.

## Release flags

Apply migrations before deploying compatible Hub replicas. Keep all flags off during the compatibility deployment, then enable only:

```text
LENS_ENDPOINT_CONNECTOR_ENABLED=true
LENS_ENDPOINT_HANDOFF_ENABLED=true
LENS_KUBERNETES_CONNECTOR_ENABLED=false
LENS_GITHUB_CONNECTOR_ENABLED=false
LENS_AWS_CONNECTOR_ENABLED=false
LENS_AZURE_CONNECTOR_ENABLED=false
LENS_GCP_CONNECTOR_ENABLED=false
```

Wait until every Hub replica is compatible before deploying the new UI. Compare the old overview totals, the executive summary, filtered findings, and filtered inventory against direct database invariants before exposing the new overview.

## Edge and application controls

Enforce distributed rate limits at the public edge for these route families. Key authenticated routes by workspace and collector routes by source; key unauthenticated routes by the edge’s verified client identity. Start with limits no less permissive than the application safety net:

| Route | Application limit |
|---|---:|
| `POST /v1/workspaces/bootstrap` | 10 per 5 minutes |
| `POST /v1/environments/setup-sessions` | 30 per 5 minutes |
| `POST /v1/environments/{id}/handoffs` | 30 per 5 minutes |
| `POST /v1/public/endpoint-handoffs/resolve` | 60 per 5 minutes |
| `POST /v1/enrollment/exchange` | 60 per 5 minutes |
| `POST /v1/discovery/snapshots` | 120 per minute |

The Hub stores only a SHA-256 key digest in its shared rate-limit table. Snapshot bodies remain limited to 32 MiB. A PostgreSQL advisory lock permits one active normalization job per source, returning `429 source_busy` with `Retry-After` when backpressure is active. The public installer and handoff resolver return `Cache-Control: no-store` and `Referrer-Policy: no-referrer`.

Do not log discovered names, evidence, paths, enrollment codes, access tokens, refresh tokens, handoff fragments, or rate-limit identifiers. Alert on normalization queue depth and age, setup errors, handoff expiry, authorization failures, 429 volume, notification failures, database commit failures, and 5xx rates.

## Acceptance

Run these journeys on clean macOS, Windows, and Linux machines:

1. Create an account, enter an organization name, and confirm the creator is owner.
2. Reach endpoint setup within two minutes, excluding external email verification.
3. Complete a direct install using only the in-product platform instructions.
4. Leave, reload, and open the same workspace as another administrator while activation is awaiting install, processing, ready, partial, failed, stale, and disconnected.
5. Create an IT handoff, rotate its command, verify the earlier command is rejected, and confirm enrollment closes the handoff.
6. Verify explicit handoff revocation, 24-hour expiry, single use, and environment disconnect reject enrollment.
7. Confirm results appear only after normalization commits and create one durable in-app notification for ready, partial, or terminal failure.
8. Confirm Overview totals open matching filtered lists, the highest-priority finding opens its evidence detail, and direct links survive reload and browser back.
9. Test dialogs with keyboard-only input: named dialog, initial focus, contained Tab order, Escape close, and focus restoration.
10. Confirm a sole owner cannot delete their identity before transferring ownership or deleting the workspace.

Run `go test -race ./...`, `go vet ./...`, the UI unit and Playwright suites, OpenAPI lint, production builds, Helm lint, and `govulncheck -mode=binary` for `barrikade-lens`, `lens-hub`, and `lens-k8s`. Public signup remains closed until all gates pass.

## Rollback

Disable the endpoint handoff and CISO overview flags if authorization, cross-view counts, signup-to-first-result conversion, or first-result latency regresses. Disable the endpoint connector if enrollment or ingestion integrity regresses. Leave additive schema in place during rollback.
