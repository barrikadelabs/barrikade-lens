# Contributing

Keep Lens within discovery. Changes that add policy, scoring, blocking, approval, credential storage, prompt/tool telemetry, or governance belong outside this repository.

Every detector change needs fixtures for positive, absent, malformed, unreadable, duplicate, and platform-specific cases. Privacy changes need a serialization test proving private values cannot leave the collector. Prefer declarative signatures; built-in Go analyzers are reserved for structured formats and correlation.

## Before you start

Read the [architecture](docs/architecture.md), [privacy boundary](docs/privacy.md),
[data-integrity rules](docs/data-integrity.md), and [threat
model](docs/threat-model.md) before changing discovery or ingestion behavior.
Detector contributors should also read [detector packs](docs/detector-packs.md).

Use Go 1.26, Node.js 24 or newer, and npm 11 or newer. Install JavaScript
dependencies without lifecycle scripts:

```sh
npm ci --ignore-scripts
```

## Validate a change

Run the checks relevant to the files you changed. Before submitting a broad
change, run the complete local suite:

```sh
go test ./...
npm test
npm run typecheck -w @barrikade/lens-hub-ui
npm run test:e2e -w @barrikade/lens-hub-ui
go build ./cmd/barrikade-lens ./cmd/lens-hub ./cmd/lens-k8s
```

PostgreSQL integration tests run when `LENS_TEST_DATABASE_URL` is set. Changes
to the native collector or packaging must also cross-compile the CLI for macOS,
Linux, and Windows. See [release integrity](docs/releasing.md) for artifact and
signing requirements.

## Documentation

Update the relevant guide when behavior, configuration, flags, commands, or
operator responsibilities change. Add new guides to the [documentation
index](docs/README.md), use product-facing navigation labels, and keep examples
free of real credentials, enrollment codes, customer data, and private
infrastructure identifiers.
