# Barrikade Lens on Google Cloud

This directory creates isolated Google Cloud foundations and partner-specific Lens Hub stacks.

## Layout

- `bootstrap/`: one local-state run for test and one for production; creates only the project and its protected Terraform state bucket.
- `foundation/`: uses the new remote state bucket; creates enabled APIs, Artifact Registry, budget, and repository/environment-restricted GitHub deployment identity.
- `partner/`: one state per design partner and environment; creates the runtime identity, PostgreSQL instance, database, secrets, Cloud Run service, public health check, and alert policy.
- `config/`: reviewed input examples. Copy the examples outside source control before adding real values.

## Apply order

1. Choose globally unique project IDs and fill the two foundation variable files.
2. Apply `bootstrap/` once and store its small local state in an encrypted company archive.
3. Copy the printed backend bucket name into a backend config and apply `foundation/` using that remote backend.
4. Build and push the Lens image, recording its immutable `sha256:` digest.
5. Fill a partner variable file and apply `partner/` with its own state prefix.
6. Point the partner hostname to the resulting Cloud Run or load-balancer endpoint only after the launch checks pass.

Example commands are intentionally not wrapped in a script so each production action remains visible:

```sh
terraform -chdir=bootstrap init
terraform -chdir=bootstrap plan -var-file=../config/test.foundation.tfvars
terraform -chdir=bootstrap apply -var-file=../config/test.foundation.tfvars

terraform -chdir=foundation init \
  -backend-config=../config/test.foundation.backend.hcl
terraform -chdir=foundation plan -var-file=../config/test.foundation.tfvars
terraform -chdir=foundation apply -var-file=../config/test.foundation.tfvars

terraform -chdir=partner init \
  -backend-config=../config/test.partner.backend.hcl
terraform -chdir=partner plan -var-file=../config/test.partner.tfvars
```

Use a distinct backend prefix for foundation and every partner/environment state. Partner state contains the generated database password and JWT secret; the state bucket therefore enforces public-access prevention, uniform access, versioning, and a retention policy. Never commit `.tfvars`, `.tfstate`, backend files, OIDC secrets, or GitHub App keys.

Production apply is expected to run only after test promotion and a protected GitHub `production` environment approval. The federation condition checks the repository owner ID, exact repository, and exact GitHub environment claim.

## Intentional launch gaps

The module initially permits public Cloud Run HTTPS because collectors, OIDC callbacks, and GitHub webhooks are external. Before a production partner launch, add a global external Application Load Balancer and Cloud Armor, verify it, then restrict Cloud Run ingress to the load balancer.

`/health` is used for basic uptime because Cloud Run reserves some paths ending in `z`. Add a database-aware `/ready` route and an external alert notification channel before treating the stack as production-ready.

## Pause and resume an environment

`suspended = true` blocks unauthenticated Cloud Run requests, sets its minimum instance count to zero, and disables the uptime alert. `database_suspended = true` stops Cloud SQL while preserving its data and backups. Retained storage, registry, secret, and state costs continue.

Pause in two applies so the application revision becomes ready before PostgreSQL stops:

```sh
terraform -chdir=partner apply \
  -var-file=../config/prod.partner.tfvars \
  -var='database_suspended=false'
terraform -chdir=partner apply \
  -var-file=../config/prod.partner.tfvars
```

For the first apply, set `suspended = true` and `database_suspended = true` in the production variable file; the command-line override temporarily keeps the database running. The second apply removes that override and stops it.

Resume in the reverse data-safe order. First apply with `database_suspended=false` while `suspended` remains true. After the database is running, set both values to `false` and apply again. Verify `/health` before restoring monitoring notifications or partner traffic.
