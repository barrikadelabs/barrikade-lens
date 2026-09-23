# Lens documentation

Start with the guide that matches what you are trying to do. The root
[README](../README.md) covers the product, a local scan, and the development
quick start.

## Deploy and operate Lens

| Goal | Guide |
|---|---|
| Run Lens Hub in your own environment | [Self-hosting](self-hosting.md) |
| Operate the Barrikade managed deployment | [Managed self-serve](managed-self-serve.md) |
| Deploy the managed service to Azure | [Azure production deployment](azure-deployment.md) |
| Roll out endpoint collectors with MDM | [Fleet rollout](fleet-rollout.md) |
| Operate the endpoint beta | [Endpoint beta operations](endpoint-beta-operations.md) |
| Install and operate Kubernetes discovery | [Kubernetes discovery](kubernetes-discovery.md) |
| Configure the GitHub App | [GitHub App discovery](github-app.md) |
| Scan repositories in other CI systems | [CI repository scanning](ci-scanning.md) |
| Release and verify artifacts | [Release integrity](releasing.md) |
| Reproduce the million-entity performance gate | [Scale testing](scale-testing.md) |

## Understand and extend Lens

| Topic | Guide |
|---|---|
| Components, data flow, and trust boundaries | [Architecture](architecture.md) |
| Entity identity, aggregation, and executive projections | [Data integrity](data-integrity.md) |
| Relationship and capability semantics | [Evidence topology](evidence-topology.md) |
| Detector format and quality requirements | [Detector packs](detector-packs.md) |
| Security boundaries and operator responsibilities | [Threat model](threat-model.md) |

Contributors should also read the repository [contribution guide](../CONTRIBUTING.md)
and [security policy](../SECURITY.md).

## Privacy and analytics

| Topic | Guide |
|---|---|
| Collector privacy boundary and retained evidence | [Privacy and evidence](privacy.md) |
| Managed product analytics contract | [PostHog analytics](posthog-analytics.md) |
| Access, deletion, and retention procedures | [Privacy operations](privacy-operations.md) |

The product notice served by a managed Lens deployment remains authoritative
for its users. These repository documents describe implementation and operating
requirements.
