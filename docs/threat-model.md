# Collector and Hub threat model

Primary protected assets are organizational inventory, identity metadata, collector refresh credentials, OIDC sessions, webhook secrets, and repository access tokens.

Trust boundaries are endpoint-to-Hub ingestion, browser-to-Hub queries, GitHub webhooks/archive fetches, Kubernetes API reads, catalog fetches, and outgoing webhooks.

Controls include short-lived scoped JWTs, rotating one-time refresh credentials, organization filters on every query, HMAC webhook signatures and delivery replay IDs, OIDC ID-token verification with PKCE, bounded request/document/archive sizes, archive traversal protection, schema privacy rejection, SSRF address pinning, metadata-address blocks, strict active-probe allowlists, read-only Kubernetes RBAC, and zero Secret/exec permissions.

Lens does not execute discovered tools, run repository code, install runtime hooks, capture prompts, or inspect secret values. Declarative detector packs cannot execute code. Repository archives and successful raw snapshots are not retained.

Operators must terminate TLS, protect PostgreSQL, use external secret management, restrict bootstrap administration to quickstarts, rotate GitHub/OIDC credentials, and monitor failed ingestion/webhook queues.
