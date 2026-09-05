# Azure deployment

The Lens pilot runs as an Azure Container App backed by Azure Database for
PostgreSQL. GitHub Actions builds and deploys the Hub image after the `ci`
workflow succeeds on `main`.

## Deployment flow

1. `ci` tests the exact commit pushed to `main`.
2. `deploy-azure` checks out that tested commit.
3. GitHub authenticates to Azure through OpenID Connect. No Azure client secret
   is stored in GitHub.
4. The workflow builds the Hub image and pushes the immutable
   `sha-<git-sha>` tag to Azure Container Registry.
5. Azure Container Apps creates a revision and keeps the previous revision live
   until the new revision is ready.
6. The workflow verifies the ready revision and calls `/healthz`.

Deployments are serialized through the `azure-pilot` concurrency group. A
failed CI run never starts a deployment. The workflow can also be started
manually from GitHub Actions; a manual run deploys the commit containing the
workflow that was selected in the UI.

## GitHub environment

The workflow uses an `azure-pilot` GitHub environment with these variables:

- `AZURE_CLIENT_ID`: client ID of the deployment managed identity
- `AZURE_TENANT_ID`: Microsoft Entra tenant ID
- `AZURE_SUBSCRIPTION_ID`: target Azure subscription ID

The federated credential must trust this subject:

```text
repo:barrikadelabs/barrikade-lens:environment:azure-pilot
```

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
