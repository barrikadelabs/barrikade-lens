# Barrikade Lens

Run `npx barrikade-lens` for a guided, local-first discovery scan. The npm launcher selects a native `@barrikade/lens-*` platform package through npm optional dependencies and never downloads executable code during install or startup.

For a Lens Hub enrollment, use the single-device command generated on its Coverage page. Adding `--install` enrolls the endpoint and starts the managed background collector in the same command:

```sh
npx --yes barrikade-lens enroll ABCDE-FGHIJ --hub https://lens.example.com --install
```

MVP native binaries are currently unsigned. Prefer the npm launcher, verify release checksums when downloading binaries directly, and expect platform signing and managed installers in a later production-hardening release.
