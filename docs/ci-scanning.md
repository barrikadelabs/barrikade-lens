# Repository discovery in CI

GitHub uses the native read-only App. GitLab, Bitbucket, and other source-control systems can run the generic scanner in an existing checkout; Lens neither archives nor uploads source bodies.

```sh
npx barrikade-lens scan --scope repo --path . --format ndjson --output lens.ndjson
```

Send the resulting Lens JSONL to your own artifact store or translate it into `DiscoverySnapshot v1` for `POST /v1/discovery/snapshots` using a repository-scoped collector credential. Keep that credential in the CI secret store. The scanner exports repository-relative locators and content hashes, not source content.

CycloneDX 1.7 is available when a downstream software inventory expects a BOM:

```sh
npx barrikade-lens scan --scope repo --path . --format cyclonedx --output lens.cdx.json
```
