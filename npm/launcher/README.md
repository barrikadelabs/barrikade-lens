# Barrikade Lens

Run `npx barrikade-lens` for a guided, local-first discovery scan. The npm launcher selects a native `@barrikade/lens-*` platform package through npm optional dependencies and never downloads executable code during install or startup.

For Lens Hub enrollment, use the single-device command generated from
**Coverage**. Adding `--install` enrolls the endpoint and starts the managed
background collector in the same command:

```sh
npx --yes barrikade-lens@latest enroll ABCDE-FGHIJ --hub https://lens.example.com --install
```

The npm launcher is intended for local evaluation and one-off enrollment.
Managed fleets should use the signed native pkg, MSI, deb, or rpm artifacts.
Verify release checksums and provenance before distributing any artifact.
