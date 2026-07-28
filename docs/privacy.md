# Privacy and evidence

The collector may serialize factual posture: installed, configured, running at scan time, enabled state, transport, sanitized hosts, package/image identifiers, environment-key names, repository-relative locators, and evidence hashes.

It must never serialize configuration bodies, source bodies, prompts, environment values, credential values, URL userinfo/query/fragment data, shell history, or full command arguments. Absolute local paths are converted to organization-salted SHA-256 locators. Repository-relative paths are retained because they are useful ownership and correlation evidence.

Schema validation recursively rejects sensitive field names such as password, token, secret, API key, authorization, cookie, prompt, content, and command arguments. Presence/count suffixes are permitted, for example `credential_present` and `environment_key_count`.

Repository source is processed ephemerally. GitHub archives are bounded, extracted into a private temporary directory, scanned, and removed. Kubernetes ConfigMap bodies stay in controller memory and are reduced to hashes and discovered metadata; the controller cannot read Secrets.

Agent and skill Markdown is reduced locally to validated names, descriptor state, sanitized locators, and content hashes. Lens does not serialize frontmatter descriptions, instruction bodies, or arbitrary Markdown. MCP configuration is reduced to server names, normalized transports, sanitized endpoints, enabled state, environment-key names, and credential presence; commands, arguments, headers, and values are not retained.

Local scans make no network calls unless the operator explicitly supplies an active probe. Managed collectors contact only their configured Hub. Public-catalog traffic originates only from Hub.
