# Release integrity

Tagging `vMAJOR.MINOR.PATCH` starts a fail-closed release. The workflow will not publish unsigned macOS or Windows fleet artifacts: preflight requires every signing and notarization setting before any release job runs.

## Release configuration

Configure these GitHub Actions secrets:

- `NPM_TOKEN`
- `APPLE_CERTIFICATE_P12` (base64-encoded Developer ID certificate bundle) and `APPLE_CERTIFICATE_PASSWORD`
- `APPLE_NOTARY_KEY_P8` (base64-encoded App Store Connect API key), `APPLE_NOTARY_KEY_ID`, and `APPLE_NOTARY_ISSUER_ID`
- `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, and `AZURE_SUBSCRIPTION_ID` for GitHub OIDC to Azure Artifact Signing

Configure these repository variables:

- `APPLE_APPLICATION_IDENTITY` and `APPLE_INSTALLER_IDENTITY`
- `AZURE_ARTIFACT_SIGNING_ENDPOINT`, `AZURE_ARTIFACT_SIGNING_ACCOUNT`, and `AZURE_ARTIFACT_SIGNING_PROFILE`

Use an Azure federated credential restricted to this repository and release workflow. No Apple private key, App Store Connect key, Azure credential, Hub URL, or enrollment code belongs in source, package definitions, artifacts, or release logs.

## Published artifacts

The pipeline:

1. Runs GoReleaser for Linux amd64/arm64 archives, deb/rpm packages, SHA-256 checksums, SPDX SBOMs, and a keyless Cosign signature over the checksum file.
2. Builds Intel and Apple-silicon binaries, signs them with Developer ID Application, combines them into a universal pkg, signs the pkg with Developer ID Installer, submits it with `notarytool`, staples the ticket, and requires Gatekeeper assessment to pass.
3. Builds x64 and ARM64 executables, signs them with Azure Artifact Signing, builds matching MSI packages with pinned WiX 4.0.6, signs the final MSI files, and requires every Authenticode status to be `Valid`.
4. Generates package SBOMs and checksums, and records GitHub build-provenance attestations for macOS and Windows assets.
5. Publishes the six signed native executables in product-prefixed `@barrikade/lens-*` optional npm packages before publishing the no-download `barrikade-lens` launcher with npm provenance.
6. Builds multi-architecture Hub and Kubernetes images with provenance/SBOM attestations, pushes them to GHCR, and signs immutable digests with Cosign.
7. Lints, packages, publishes, and attaches both Helm charts, leaving the GitHub release as a draft for human verification.

Direct signed binaries and the npm launcher remain supported for development and evaluation. The pkg, MSI, deb, and rpm artifacts are the managed-fleet path.

## Release verification

Compare an artifact with the platform checksum file before importing it into MDM. GitHub provenance can be checked with the GitHub CLI:

```sh
gh attestation verify barrikade-lens-universal.pkg --repo barrikadelabs/barrikade-lens
gh attestation verify barrikade-lens-win32-x64.msi --repo barrikadelabs/barrikade-lens
```

On macOS, verify the package signature, notarization ticket, and Gatekeeper result:

```sh
pkgutil --check-signature barrikade-lens-universal.pkg
xcrun stapler validate barrikade-lens-universal.pkg
spctl --assess --type install --verbose=2 barrikade-lens-universal.pkg
```

On Windows, verify the downloaded MSI and installed executable:

```powershell
Get-AuthenticodeSignature .\barrikade-lens-win32-x64.msi | Format-List Status,SignerCertificate,TimeStamperCertificate
Get-AuthenticodeSignature "$env:ProgramFiles\Barrikade Lens\barrikade-lens.exe" | Format-List Status,SignerCertificate,TimeStamperCertificate
```

For Linux, verify the SHA-256 file, its keyless signature, and the package metadata before install:

```sh
sha256sum --check checksums.txt
cosign verify-blob --signature checksums.txt.sig checksums.txt
dpkg-deb --info barrikade-lens_VERSION_linux_amd64.deb
rpm -qip barrikade-lens_VERSION_linux_amd64.rpm
```

The draft release review must confirm all expected architectures, checksum coverage, SBOM presence, provenance, signing identities, timestamp/notarization success, and the CI package-smoke jobs. Do not publish a partial release with a missing or invalid fleet artifact.

## Upgrade and rollback

Package reinstall and upgrade preserve the platform state directory and per-machine installation identity. macOS postinstall, Windows service installation, and Linux maintainer scripts activate the new binary against the retained configuration. CI installs each package without interaction, exercises reinstall/repair, uninstalls it, and confirms that normal removal preserves identity.

To roll back, revoke the problematic draft or public release, stop further MDM assignment, and deploy the last verified package. Windows MSI prevents an accidental downgrade; an administrator must uninstall the current MSI while preserving `%ProgramData%\Barrikade\Lens`, then install the older verified MSI. macOS and Linux package tools may install the previous verified version directly. Restart the collector and confirm `service status`; do not delete or clone state during rollback.

If a release-signing or notarization step fails, fix the credential or signing service and rerun the tag workflow. Never bypass the gate by attaching an unsigned substitute. npm versions are immutable, so reruns skip native package versions that already exist and the launcher-recovery workflow may publish only a launcher whose complete native dependency set is already present.

Supported fleet OS baselines and silent install/uninstall commands are maintained in [fleet-rollout.md](fleet-rollout.md).

## npm namespace strategy

Customer-facing one-command products use memorable unscoped package names such as `barrikade-lens`. Shared and platform-specific implementation packages use the company scope with a product prefix, such as `@barrikade/lens-darwin-arm64`. This leaves `@barrikade` available for future lifecycle products without creating a separate npm organization for every pillar.
