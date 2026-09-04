# Barrikade Lens v2

Barrikade Lens is the open-source discovery and exposure plane for autonomous agents. It finds agents, runtimes, frameworks, MCP servers, skills, models, APIs, repositories, endpoints, and deployments, then explains every finding with sanitized evidence and confidence.

Lens discovers and assesses exposure. It does not verify effective authorization, invoke tools, change credentials, remediate, enforce policy, register, approve, block, or assign a composite risk score.

## Run a local scan

```sh
npx barrikade-lens
```

The no-argument command opens a guided terminal interface in a TTY and emits canonical Lens JSON in automation. It works without signup or network access. Local scans do not send telemetry.

The local interface is organized for both executive review and engineering follow-up:

- **Overview** separates autonomous agents, agent-capable tools, and model runtimes; summarizes primary state; and surfaces factual conditions that need context.
- **Systems** shows root systems first, with supporting host applications and development runtimes clearly excluded from executive totals.
- **Capabilities** groups MCP servers, models, skills, tools, APIs, and workflows without expanding every API operation into the main view.
- **Coverage** explains what was checked, what failed or was unavailable, and how strong the collected evidence is.
- **Evidence graph** explains the strongest findings and relationship directions without exposing private local paths. Each finding says what matched and what to inspect next.

Use `1`–`5` or the left/right arrows to switch views, the up/down arrows or `j`/`k` to scroll, and `q` to leave. The layout adapts at 60, 80, and 140 columns and honors `NO_COLOR`.

“Attention” in Lens is a factual discovery queue, not a risk score. A possible-only or residual finding means the evidence needs corroboration; it does not mean the product is installed, running, vulnerable, or approved.

```sh
barrikade-lens scan --scope endpoint --format human
barrikade-lens scan --scope repo --path . --format ndjson --output lens.ndjson
barrikade-lens scan --scope repo --format cyclonedx --output agent-bom.json
barrikade-lens doctor
```

Exit code `0` means the scan completed, even if nothing was found. Exit code `2` means coverage was partial. Exit code `1` means a fatal scan or configuration failure.

Active handshakes are off by default. An explicitly allowed metadata-only probe looks like:

```sh
barrikade-lens scan --probe-url http://127.0.0.1:11434/v1/models --allow-probe-host 127.0.0.1
```

Probes reject credential-bearing URLs and metadata targets, use strict limits, and never invoke a tool.

## Organization-wide discovery

Lens Hub aggregates sources in PostgreSQL and exposes an open API, signed webhooks, Lens JSON/JSONL, and CycloneDX 1.7 exports. Its interactive evidence graph maps each fresh root system to connected inventory and supporting findings. Evidence drawers resolve the exact linked resource, then lead with what was found, where it was observed, safe descriptor facts, related entities, the detector rationale, and an investigation prompt. Validated skill findings name the skill and show its declared purpose, actionable descriptor location, scope, provider, format, and optional compatibility, license, and allowed-tool declarations. Content and locator hashes remain available as secondary integrity references.

For a local quickstart:

```sh
docker compose up --build
```

Open `http://localhost:8080` and use the quickstart token `lens-local-admin`. The compose credentials are deliberately development-only; use [the self-hosting guide](docs/self-hosting.md) for a real deployment.

For the Barrikade pilot, the compose stack opens the `org_local` tenant used by the managed collector. The [live-device customer-story demo](docs/demo-live-device.md) shows how to present one real finding and its evidence without loading sample inventory or implying that Lens makes approval decisions.

From the Hub’s Coverage page, generate the one-device install command and run it on the endpoint:

```sh
npx --yes barrikade-lens enroll ABCDE-FGHIJ --hub https://lens.example.com --install
```

The command exchanges the single-use ten-minute code, stores rotating collector credentials privately, installs a stable background collector, and starts reporting. Run it with administrator privileges for system-wide macOS or Windows coverage. Node.js 18 or newer is required for the npm launcher; managed fleets can continue to pre-position the native binary.

Managed endpoint discovery performs an initial full scan, watches only known agent/configuration/skill roots, debounces relevant filesystem changes, reconciles processes and listeners every 15 minutes, and runs a jittered daily full scan. It does not install runtime hooks or capture prompts, tool calls, or commands.

System collectors inspect eligible local user profiles rather than the service account's home. Fleet deployment templates are documented in [fleet rollout](docs/fleet-rollout.md); secrets remain in the organization's MDM or secret tooling.

The Kubernetes controller is installed from `deploy/helm/lens-k8s`. Its RBAC permits read-only `get/list/watch` for workloads, Services, Ingresses, ConfigMaps, and CRD definitions. It has no Secret or pod-exec permission.

GitLab, Bitbucket, and generic pipelines use the ephemeral [CI repository scanner](docs/ci-scanning.md). The native GitHub App starts from [the supplied manifest](deploy/github-app-manifest.json).

## Architecture

```mermaid
flowchart LR
  endpoint["Native endpoint collectors"] --> snapshot["Discovery Snapshot v1"]
  repo["GitHub App and CI scanner"] --> snapshot
  kube["Read-only Kubernetes controller"] --> snapshot
  snapshot --> hub["Lens Hub / PostgreSQL"]
  catalog["Replaceable open catalog providers"] --> hub
  hub --> ui["Discovery UI"]
  hub --> api["Open API, webhooks, exports"]
  api --> external["Any registration or control plane"]
```

- Go powers the detector engine, collectors, CLI/TUI, Kubernetes controller, Hub API, and PostgreSQL workers.
- TypeScript powers the no-download npm launcher and React Hub UI.
- PostgreSQL stores relational entities/edges plus JSONB attributes and is also the horizontally scalable job queue. There is no graph database or external queue.
- The canonical contract is [Discovery Snapshot 1.1](api/schema/discovery-snapshot-v1.json); [OpenAPI](api/openapi.yaml) describes the Hub.
- Detector signatures are declarative, checksummed YAML with no executable code.
- Catalog enrichment happens only at Hub. The bundled adapter reads a compact OAK-compatible manifest and lazily fetches only uniquely or manually linked documents. Catalogue operations are always labelled as potential—not evidence of grants or invocation.
- `LENS_EXPOSURE_ENABLED` gates the Exposure Map, context APIs, and finding worker. The local pilot compose setup enables it; Helm leaves it disabled until pilot acceptance.

## Repository layout

| Path | Purpose |
|---|---|
| `cmd/barrikade-lens` | Native CLI and managed endpoint service |
| `cmd/lens-hub` | Hub API and PostgreSQL workers |
| `cmd/lens-k8s` | Informer-based Kubernetes collector |
| `pkg/discovery` | Stable public discovery contract, privacy validation, identities |
| `internal/scanner` | Endpoint, repository, and Kubernetes analyzers |
| `internal/catalog` | Generic OAK/Git/file/directory catalog providers |
| `hub-ui` | Discovery-only React console |
| `npm` | npm launcher and platform packages |
| `deploy/helm` | Hub and Kubernetes Helm charts |

## Privacy contract

Lens accepts useful organizational identity—hostnames, OS users, repository/workload names, relative repository paths, and sanitized endpoint hosts—but rejects private content. Absolute paths become organization-salted hashes. URLs lose userinfo, query strings, and fragments. Configuration bodies, prompts, environment values, credentials, secret values, and full command arguments are forbidden by validation and property tests.

Read [privacy and evidence](docs/privacy.md), [data integrity and executive posture](docs/data-integrity.md), [architecture](docs/architecture.md), and the [threat model](docs/threat-model.md) before extending a detector.

Detector contributors should also read [detector packs and detection-quality rules](docs/detector-packs.md). Lens favors validated open formats and independent evidence over filename or product-name guesses: agent instructions are not agents, framework imports do not manufacture agents, supporting runtimes stay separate, and malformed descriptors are excluded from inventory.

## Build and test

Requirements are Go 1.26, Node.js 24+, npm 11+, and PostgreSQL 16+ for Hub integration tests.

```sh
go test ./...
npm ci --ignore-scripts
npm test
go build ./cmd/barrikade-lens ./cmd/lens-hub ./cmd/lens-k8s
```

Set `LENS_TEST_DATABASE_URL` to run the PostgreSQL stale/removal and tenant-isolation integration tests. Release builds use GoReleaser, generate SBOMs and checksums, and populate the platform-specific npm packages.

For a source checkout, install the local native development build once:

```sh
npm ci --ignore-scripts
npm run install:local
```

This stages the binary for the current OS and architecture, creates the local npm executable, and links `barrikade-lens` into npm's global bin directory. Afterward, both `npx barrikade-lens` and `barrikade-lens` run the checkout. Registry users do not need this step after the v2 platform packages and launcher are published.

Weekly and manually triggered scale CI enforces a two-second inventory-query gate at one million current entities. Signed artifact requirements and release secrets are described in [release integrity](docs/releasing.md).

## License and provenance

Lens is Apache-2.0. Select inventory, redaction, digest, and managed-deployment design lessons were ported from Agent Beacon under its MIT license; the notice is retained in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The bundled public catalog adapter treats its CC0 source as replaceable data and exposes source provenance as “Public API Catalog.”
