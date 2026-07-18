# Release integrity

Tag workflows intentionally fail unless Apple Developer ID, Apple installer/notarization, Windows Authenticode, npm, and GitHub release credentials are present. GA artifacts are never downgraded to unsigned output.

The release pipeline:

1. Runs GoReleaser for Linux archives, deb/rpm packages, checksums, SBOMs, and keyless checksum signatures.
2. Builds both macOS slices, applies hardened-runtime signatures, creates a universal pkg, notarizes it, and staples the ticket.
3. Authenticode-signs both Windows binaries and their WiX service MSIs with timestamping.
4. Publishes signed native binaries in platform-specific optional npm packages before publishing the no-download `barrikade-lens` launcher.
5. Builds multi-architecture Hub and Kubernetes images with provenance/SBOM attestations, pushes them to GHCR, and signs their immutable digests with Cosign.
6. Lints, packages, publishes, and attaches both Helm charts.

Required repository secrets are `MACOS_CERTIFICATE_P12`, `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGNING_IDENTITY`, `MACOS_INSTALLER_IDENTITY`, `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD`, `WINDOWS_CERTIFICATE_PFX`, `WINDOWS_CERTIFICATE_PASSWORD`, and `NPM_TOKEN`.
