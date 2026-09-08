# Azure deployment

The Lens pilot runs as an Azure Container App backed by Azure Database for
PostgreSQL. GitHub Actions builds and deploys the Hub image after the `ci`
workflow succeeds on `main`.

The pilot is the staging environment. The deploy workflow explicitly enables
the privacy-minimized PostHog integration, uses the EU ingestion host, and sets
`LENS_DEPLOYMENT_ENVIRONMENT=staging`. It fails before building if either
required Container App secret is missing.

## Deployment flow

1. `ci` tests the exact commit pushed to `main`.
2. `deploy-azure` checks out that tested commit.
3. GitHub authenticates to Azure through OpenID Connect. No Azure client secret
   is stored in GitHub.
4. The workflow builds the Hub image and pushes the immutable
   `sha-<git-sha>` tag to Azure Container Registry.
5. Azure Container Apps creates a revision and keeps the previous revision live
   until the new revision is ready.
6. The workflow verifies the ready revision and calls `/readyz`.

The verification request uses `/readyz` so a revision is accepted only after
the database is reachable and startup migrations have completed. It also checks
`/v1/auth/config` for Clerk self-service mode and confirms that the EU staging
analytics configuration is exposed with a browser-safe `phc_` project token.

Deployments are serialized through the `azure-pilot` concurrency group. A
failed CI run never starts a deployment. The workflow can also be started
manually from GitHub Actions; a manual run deploys the commit containing the
workflow that was selected in the UI.

To test a feature branch, first require a green pull-request CI run. Then select
`deploy-azure` in GitHub Actions, choose **Run workflow**, select the feature
branch, and run it. The manual deployment uses that branch's exact commit SHA
and immutable image tag.

After deployment, verify Clerk login, workspace bootstrap, endpoint enrollment,
a complete or partial scan, inventory navigation, Account Settings, account
opt-out, logout, and workspace switching. Verify that only allowlisted events
are sent to `eu.i.posthog.com`, then confirm that account opt-out, GPC/DNT,
logout, and workspace switching suppress or reset browser analytics as defined
in `docs/posthog-analytics.md`.

## Analytics secrets

Add `posthog-project-token` and `posthog-id-salt` as Azure Container App secrets
before deploying. Use the existing EU project token and a stable, randomly
generated salt of at least 32 bytes. Never put either value in the workflow or
repository. The deployment workflow references these secret names directly.

The first analytics-enabled revision must be limited to Barrikade's internal
test workspace and validated against the staging insights in
`docs/posthog-analytics.md`. Rollback is an explicit Container App environment
update setting `LENS_POSTHOG_ENABLED=false`, followed by a revision restart;
Lens functionality does not depend on PostHog delivery.

## GitHub environment

The workflow uses an `azure-pilot` GitHub environment with these variables:

- `AZURE_CLIENT_ID`: client ID of the deployment managed identity
- `AZURE_TENANT_ID`: Microsoft Entra tenant ID
- `AZURE_SUBSCRIPTION_ID`: target Azure subscription ID

The federated credential must trust this subject:

```text
repo:barrikadelabs@274570617/barrikade-lens@1301339073:environment:azure-pilot
```

This repository uses GitHub's ID-hardened OIDC subject prefix, so the
organization and repository numeric IDs are part of the claim. Do not replace
it with the legacy name-only `repo:barrikadelabs/barrikade-lens` prefix. Check
the repository's current subject configuration before recreating the Azure
federated credential:

```bash
gh api repos/barrikadelabs/barrikade-lens/actions/oidc/customization/sub
```

An `AADSTS700213` login failure means the issuer, audience, or complete subject
in Azure does not exactly match the assertion printed by `azure/login`.

The deployment identity only needs `AcrPush` on the pilot registry and
`Container Apps Contributor` on the pilot Container App. The Container App's
runtime identity remains separate and retains its existing `AcrPull` and Key
Vault permissions.

## Rollback

Redeploy a known-good commit from the Actions page, or update the Container App
to its existing immutable image tag:

```bash
az containerapp update \
  --resource-group rg-lens-pilot-ne \
  --name ca-lens-hub-pilot \
  --image acrlenspilotnef635.azurecr.io/lens-hub:sha-<known-good-git-sha>
```

Do not reuse or overwrite commit image tags.
