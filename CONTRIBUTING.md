# Contributing

Keep Lens within discovery. Changes that add policy, scoring, blocking, approval, credential storage, prompt/tool telemetry, or governance belong outside this repository.

Every detector change needs fixtures for positive, absent, malformed, unreadable, duplicate, and platform-specific cases. Privacy changes need a serialization test proving private values cannot leave the collector. Prefer declarative signatures; built-in Go analyzers are reserved for structured formats and correlation.

Run `go test ./...`, `npm test`, and cross-compile the native CLI for macOS, Linux, and Windows before submitting a change. PostgreSQL tests run when `LENS_TEST_DATABASE_URL` is set.
