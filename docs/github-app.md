# GitHub App repository discovery

Create a GitHub App with read-only Repository metadata and Contents permissions. Subscribe to installation, repository, and push webhooks. Point the webhook URL at `/v1/connectors/github/webhook` and configure the same HMAC secret as `LENS_GITHUB_WEBHOOK_SECRET`.

`deploy/github-app-manifest.json` is a starting manifest. Replace its example webhook host before creating the App.

Set `LENS_GITHUB_APP_ID` and `LENS_GITHUB_PRIVATE_KEY_FILE` on Hub. Installations map to the Hub’s default organization in single-organization mode.

Push webhooks enqueue a commit scan. A nightly reconciliation enumerates accessible repositories to cover missed webhooks. Hub obtains a short-lived installation token, downloads a bounded archive for the exact commit, scans it in a private temporary directory, and deletes it. Source is never inserted into PostgreSQL or sent by a scanner.
