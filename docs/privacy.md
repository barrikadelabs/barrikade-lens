# Privacy and evidence

The collector may serialize factual posture: installed, configured, running at scan time, enabled state, transport, sanitized hosts, package/image identifiers, environment-key names, repository-relative locators, and evidence hashes.

It must never serialize configuration bodies, source bodies, prompts, environment values, credential values, URL userinfo/query/fragment data, shell history, or full command arguments. Absolute local paths are converted to organization-salted SHA-256 locators. Repository-relative paths are retained because they are useful ownership and correlation evidence.

The UI does not present an opaque path hash as the finding. It labels the location as protected, explains the safe facts that matched, and keeps the hash in a secondary integrity section. Repository-relative paths, skill-root-relative descriptor paths, and sanitized network endpoints remain visible when they are safe and actionable. For a valid `SKILL.md`, Lens retains a bounded declarative envelope: the exact skill name, declared purpose, format, scope, provider, license, compatibility, allowed-tool selectors, and frontmatter field names when present. The instruction body and arbitrary metadata values remain excluded.

Schema validation recursively rejects sensitive field names such as password, token, secret, API key, authorization, cookie, prompt, content, and command arguments. Presence/count suffixes are permitted, for example `credential_present` and `environment_key_count`.

Repository source is processed ephemerally. GitHub archives are bounded, extracted into a private temporary directory, scanned, and removed. Kubernetes ConfigMap bodies stay in controller memory and are reduced to hashes and discovered metadata; the controller cannot read Secrets.

Agent Markdown is reduced locally to validated names, descriptor state, sanitized locators, and content hashes. Skill Markdown additionally retains the bounded declarative envelope above so an operator can understand what a discovered skill claims to do. Lens does not serialize skill instruction bodies, agent instruction bodies, arbitrary Markdown, or arbitrary metadata values. MCP configuration is reduced to server names, normalized transports, sanitized endpoints, enabled state, environment-key names, and credential presence; commands, arguments, headers, and values are not retained.

Local scans make no network calls unless the operator explicitly supplies an active probe. Managed collectors contact only their configured Hub. Public-catalog traffic originates only from Hub.
