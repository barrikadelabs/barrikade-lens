# MVP release integrity

Lens MVP releases optimize for a dependable one-command npm experience without paid platform-signing prerequisites. macOS and Windows binaries are currently unsigned, and native `.pkg`/MSI installers are deferred until managed enterprise distribution is production-hardened.

The release pipeline:

1. Runs GoReleaser for Linux archives, deb/rpm packages, checksums, SBOMs, and keyless checksum signatures.
2. Builds unsigned macOS and Windows binaries for the six supported OS/architecture combinations.
3. Publishes native binaries in product-prefixed `@barrikade/lens-*` optional npm packages before publishing the no-download `barrikade-lens` launcher.
4. Builds multi-architecture Hub and Kubernetes images with provenance/SBOM attestations, pushes them to GHCR, and signs their immutable digests with Cosign.
5. Lints, packages, publishes, and attaches both Helm charts.
6. Creates a draft GitHub release for human verification before it is marked public.

Only the `NPM_TOKEN` repository secret is required. For the initial publication it must be a granular npm token owned by an account that can publish the existing unscoped `barrikade-lens` package and new packages in the `@barrikade` organization. The token must have package write access and be permitted to bypass 2FA for non-interactive publishing.

After the initial packages exist, configure npm Trusted Publishing for `.github/workflows/release.yml` and remove the long-lived token from the workflow and repository secrets.

The workflow publishes final versions with the `latest` npm dist-tag and prerelease versions with `next`. Reruns skip npm package versions that already exist because npm versions are immutable.

## MVP limitation

Directly downloaded macOS or Windows binaries may trigger Gatekeeper or SmartScreen warnings. Prefer `npx barrikade-lens`, and verify GitHub release checksums for direct downloads. Restore Apple notarization, Authenticode, and signed managed installers before treating native fleet packages as enterprise GA artifacts.

## npm namespace strategy

Customer-facing one-command products use memorable unscoped package names such as `barrikade-lens`. Shared and platform-specific implementation packages use the company scope with a product prefix, such as `@barrikade/lens-darwin-arm64`. This leaves `@barrikade` available for future lifecycle products without creating a separate npm organization for every pillar. The `@barrikade-lens` organization may remain reserved but is not required by Lens v2.
