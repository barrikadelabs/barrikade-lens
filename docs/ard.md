# ARD declarations and Lens discovery

Lens uses Agentic Resource Discovery as a declaration input and export format. It
does not become an ARD Registry and does not implement semantic search,
federation, DNS discovery, open-web crawling, or runtime connection.

## What changes for a Lens user

Without ARD, Lens answers: “What agentic systems and resources did collectors
actually observe?”

With ARD, Lens also answers: “What resources did publishers declare, and where
does exact identity evidence connect those declarations to observations?”

Declared resources remain `resource_declaration` artifacts in the graph. They
cannot increase autonomous-agent, agent-tool, or model-runtime totals and
cannot change an observed system’s running state, network scope, ownership, or
confidence.

The Hub presents four alignment facts:

- `matched`: one exact observed resource.
- `declared_only`: published, but no exact observation.
- `conflict`: exact facts identify more than one observed candidate.
- `observed_only`: an eligible observed resource has no exact declaration.

Names, descriptions, capabilities, and shared hosts are insufficient for an
automatic link. Name agreement is retained only as a suggestion.

## Adding a catalog

An administrator opens **Declarations**, enters an explicit catalog URL,
validates it, reviews the entry/media-type summary, and saves it. The Hub—not
collectors—refreshes the source every six hours by default. Conditional ETag
and Last-Modified requests avoid unchanged work.

Catalog sources become stale after 24 hours without a successful refresh.
Failed requests preserve current inventory. Successful full refreshes use the
same two-miss stale and three-miss removal semantics as other Lens sources.

Same-site nested catalogs are followed to depth 3, at most 100 catalogs and
25,000 declarations per configured source. Cross-site nested catalogs remain
visible but must be configured separately. Registries are inventoried but their
search/list endpoints are not called.

## Repository behavior

`barrikade-lens scan --scope repo` recognizes:

- `.well-known/ai-catalog.json` and catalog-oriented ARD JSON files.
- `Agentmap` entries in `robots.txt` that resolve to a file in the repository.
- HTML `<link rel="ai-catalog">` references that resolve to a file in the
  repository.

Repository scanning never follows a remote reference. It stores sanitized
repository-relative evidence locators and hashes, not source bodies.

## Privacy and trust language

Lens retains bounded display metadata, publisher domain, media type, version,
updated time, normalized tags/capability names, safe descriptor facts, and
sanitized artifact URL/host.

Lens discards raw inline documents, representative queries, arbitrary metadata,
full signatures, attestation bodies, prompts, credentials, configuration
bodies, environment values, and command arguments. A signature can be
`present_unverified` or `malformed`; Lens stores only its digest. Attestation
types are publisher-claimed facts. `aligned` means the claimed identity’s trust
domain agrees with the ARD publisher namespace—it does not mean cryptographic
verification was performed.

## Manual export

`POST /v1/exports/ard` is stateless. The UI exposes it as **Download
ai-catalog.json** from a declaration. Every selected entry needs:

- A valid `urn:air:` identifier under the supplied publisher domain.
- An artifact media type.
- A credential-free HTTPS artifact URL already discovered or explicitly
  supplied.

Lens returns a file but never saves or publishes it and does not expose a
`/.well-known/ai-catalog.json` endpoint.

## Operations and boundaries

Set `LENS_ARD_ENABLED=false` to disable the ARD APIs, UI, and remote catalog
synchronization. Internal hosts require an exact
`LENS_ARD_PRIVATE_HOST_ALLOWLIST`; the default is empty.
Requests use a 20-second timeout, an 8 MiB body limit, safe same-site redirects,
and address checks that block localhost, private/link-local ranges, and cloud
metadata.

The canonical Lens graph and exports include catalog/declaration nodes. ARD is
an interoperability input/output for discovery only—not registration,
approval, policy, protection, governance, or runtime observability.
