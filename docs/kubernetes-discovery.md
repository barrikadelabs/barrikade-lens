# Kubernetes discovery operations

The Kubernetes collector is feature-gated by
`LENS_KUBERNETES_CONNECTOR_ENABLED`. Keep the managed-production gate disabled
until deployment evidence for the target cluster has been reviewed.

## Install and enroll

Create a one-use Kubernetes setup in Lens, then install the chart with the Hub
URL and enrollment code returned by the setup flow:

```sh
helm upgrade --install lens-k8s deploy/helm/lens-k8s \
  --namespace barrikade-lens --create-namespace \
  --set-string hubURL=https://lens.example.com \
  --set-string enrollmentCode=REDACTED
```

The controller derives stable cluster identity from the `kube-system` namespace
UID unless `clusterID` is supplied. A persistent volume holds the enrolled
collector identity and rotated refresh credential. No database edit is required.
Use `existingSecret` instead of inline values when a secret manager provisions a
bootstrap `config.json`.

Verify the deployment, then check Coverage for a Kubernetes target and the first
full reconciliation:

```sh
kubectl -n barrikade-lens rollout status deployment/lens-k8s
kubectl -n barrikade-lens logs deployment/lens-k8s --since=10m
```

## Coverage and recovery

Informer events produce debounced incremental snapshots. A six-hour full
reconciliation repairs missed events and state after restarts or Hub outages.
If one informer cache cannot synchronize, the controller uploads the surfaces it
can read and marks the scan partial instead of reporting complete coverage.
Denied referenced ConfigMaps and malformed MCP entries also appear as bounded
coverage errors; object bodies and error details are not uploaded.

Only referenced ConfigMap bodies are fetched. Secrets are represented by name as
credential references and are never read. The shipped role has no Secret,
`pods/exec`, mutation, impersonation, token-request, or wildcard permission.

## Stable correlation

Clusters and workloads use Kubernetes UIDs. Container image digests are retained
when present. Workloads correlate to a repository only through the explicit
`barrikade.ai/repository-url` or `org.opencontainers.image.source` label; the
optional `org.opencontainers.image.revision` label records a commit. Display
names are never correlation keys.

## Upgrade, credential rotation, and uninstall

Use `helm upgrade` with an immutable image tag. The Deployment uses `Recreate`
because the state volume is single-writer. The persistent configuration is
forward compatible within snapshot 1.x; a collector that finds an older config
version stops with an explicit re-enrollment error rather than inventing a new
cluster identity.

Collector access and refresh credentials can be revoked from Lens. A revoked
collector cannot refresh or upload. To rotate bootstrap configuration supplied
through `existingSecret`, update the Secret and restart the Deployment.

`helm uninstall lens-k8s --namespace barrikade-lens` removes the collector,
service account, read-only cluster role/binding, enrollment Secret, and chart
PVC. Confirm the storage class's reclaim behavior if retained storage is a local
requirement. Uninstall does not erase historical Hub evidence immediately; use
the Lens connection lifecycle to disconnect/revoke the source and apply the
configured retention policy.

## Gate evidence

Before enabling the feature in production, retain results for clean install,
first full scan, RBAC negative tests, a missed-event/full-reconciliation test,
Hub outage recovery, restart with the same cluster ID, credential revocation,
upgrade, uninstall, and a representative scale run. `go test ./...` includes the
contract, privacy, topology, and shipped-RBAC regressions; cluster lifecycle
evidence must be captured against the target Kubernetes versions.
