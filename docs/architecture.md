# Architecture

Lens has one language-neutral contract and three discovery surfaces.

Endpoint collectors inspect known filesystem roots, package/configuration descriptors, process names, and listening ports. Repository scanners inspect manifests, lockfiles, imports, agent declarations, MCP/A2A/OpenAPI/Arazzo artifacts, CI, container, and infrastructure files. Kubernetes informers reduce workload metadata and referenced ConfigMaps into the same graph without Secret access or exec.

Every detector emits evidence before it emits an entity. A scanner must explicitly mark an observation authoritative after validating its product or protocol shape; method labels and filenames carry no implicit authority. A validated authoritative descriptor confirms a finding. Otherwise, confirmation requires two independent high-specificity evidence families. A lone high-specificity observation is `likely`; lower-specificity evidence is `possible`. Display names are never correlation keys.

Detection separates facts that are often confused. Repository instructions are repository context, framework dependencies are framework use, supporting runtimes remain supporting runtimes, and only explicit agent definitions or agent-intent metadata create agents. Kubernetes workloads using Node.js or Python do not become agents by association. Unknown future runtimes can still be discovered through explicit workload labels, A2A/MCP documents, custom-agent descriptors, and open Agent Skills without waiting for a named product signature.

Entity IDs are deterministic and organization-scoped. An endpoint target is derived from the fingerprint of a persistent Ed25519 installation identity; collector credentials can rotate without changing target, entity, or relationship IDs. Cross-surface keys use repository URLs and commits, image digests, Kubernetes UIDs, configuration fingerprints, and explicit labels. PostgreSQL stores current inventory separately from per-source observations and 90-day evidence/change history.

Ingestion is a PostgreSQL job queue claimed with `FOR UPDATE SKIP LOCKED`. Normalization and presence reconciliation share one transaction. Successful raw JSON is nulled immediately; failed payloads expire within 24 hours. Full snapshots advance omission counters: two consecutive misses mark a source observation stale and three remove it from current inventory.

The Hub recomputes organization-level entities from all current source observations. Boolean facts merge with OR, sets by normalized union, and scalar conflicts use the highest-confidence newest observation while remaining visible as data-quality diagnostics. Indexed posture projections power overview and paginated investigation APIs without replacing the canonical graph.

Catalog enrichment is a fail-open Hub worker. Providers implement refresh, match, and lazy fetch. Exact and high-confidence host/provider matches link automatically; fuzzy candidates remain suggestions. Unavailability never blocks discovery ingestion or queries.

Lens Hub is an interoperable producer, not a control plane. Its APIs and HMAC webhooks are designed to feed registration or governance systems without importing those responsibilities into Lens.

For the Registry pilot, the macOS endpoint collector may emit the separate experimental `runtime-identity-observation-v0.1` evidence envelope for a running Codex process. It distinguishes an installed runtime from a process instance and contains PID/start-time lineage, UID, executable digests and macOS signing facts. The envelope and its Snapshot 1.1 projection are explicitly non-authoritative: Registry Runtime must independently re-observe the process before it can bind a session, and Lens never admits, attests, or authorizes it.
