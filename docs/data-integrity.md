# Data integrity and executive posture

Lens JSON is the canonical evidence graph. The Hub's Overview, Systems, Coverage, and Changes views are indexed projections built from that graph; they are not a second source of truth and they do not assign risk or recommend action.

## Identity

`target_id` identifies the endpoint, repository, or cluster being scanned. `source_id` identifies the active collector credential. Endpoint target IDs are opaque, organization-scoped derivatives of a per-Hub Ed25519 public-key fingerprint. Hostnames are display metadata only. Two identities with the same hostname stay separate and appear as a duplicate-identity diagnostic; a hostname change does not change identity.

Repository and Kubernetes identities use stable repository and cluster identifiers. Cross-surface correlation may use repository URLs, commit SHAs, image digests, workload UIDs, configuration fingerprints, and explicit labels. It never correlates by display name alone.

## Fact aggregation

Each source retains its current sanitized entity and relationship observation. Organization-level facts are recomputed deterministically:

- Boolean values merge with OR.
- Lists merge by normalized union.
- Equal scalar values remain one fact.
- Conflicting scalars select the highest-confidence newest observation; every conflicting value and source remains visible in a data-quality record.
- Removing one source recomputes the entity from remaining observations instead of deleting or preserving the removed source's facts.

Source-specific facts remain on source relationships where appropriate. Examples include model cache/provider details and Kubernetes deployment state.

## Executive classification

Root-system counts include explicit autonomous agents, agent-capable tools, and model runtimes. Supporting development runtimes, host applications, cached models, and technical artifacts remain available in Technical inventory but do not inflate the executive footprint.

Overview, Systems, Evidence graph, and the default Technical inventory view use currently reporting targets. Stale target observations are retained rather than merged by hostname or silently deleted; an explicit Reporting filter and Coverage diagnostics expose them. This prevents a replaced endpoint identity from doubling the apparent active inventory while preserving the identity record for investigation.

The strongest factual state is selected in this order: running, deployed, defined, configured, installed, residual, cached, observed. A root system's network scope also incorporates directly connected service bindings: loopback, endpoint network binding, explicitly external Kubernetes service/ingress, none, or unknown.

An OS-user relationship is labeled **Observed user**. It becomes ownership attribution only when authoritative ownership evidence is explicitly present.

## Coverage and change

Coverage reports one row per target with collectors nested beneath it. Endpoint freshness is 60 minutes, repository freshness is 36 hours, and Kubernetes freshness is 12 hours. A coverage percentage exists only when an administrator supplies a manual expected-population baseline.

Changes are written only when normalized material state changes. Routine timestamps and repeated observations are excluded. Safe field-level differences classify changes as state, network scope, attribution, capability, confidence, identity, or freshness. Investigation APIs collapse superseded observations of the same evidence location into the latest fact and report repeat observations. They resolve every observation to its exact linked resource where possible—for example the named skill rather than its parent runtime—and add a bounded explanation layer: finding title, source target, safe matched facts, related entities, detector rationale, and investigation prompt. Validated skill evidence includes its declared purpose, descriptor format and location, scope, provider, declared license and compatibility, allowed-tool selectors, and descriptor field inventory when present. Path-only signals are explicitly labeled unvalidated and never presented as descriptor-backed resources.
