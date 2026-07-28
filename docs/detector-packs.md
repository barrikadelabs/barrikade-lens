# Detector packs

Detector packs are schema-versioned YAML data. Schema v2 requires every runtime signature to declare one of `agent_tool`, `model_runtime`, `host_application`, `development_runtime`, or `unclassified`. They can declare paths, configuration formats, process names, container-image repositories, environment-key names, IDE extension IDs, product-specific skill roots, user-agent roots, local model-server ports, known listeners, local model-cache layouts, framework packages, and import names. Packs can also declare portable Agent Skills roots and IDE extension roots independently of any vendor runtime. They contain no executable expressions, scripts, or hooks.

Use `barrikade-lens --detector-pack ./pack.yaml` to load a local pack. Lens validates IDs, configuration formats and scopes, cache layouts, ports, duplicate signatures, schema version, and an optional structural SHA-256 checksum before scanning. Hub distribution can deliver the same bytes; the collector still performs local validation.

New high-confidence detectors should use a validated, authoritative descriptor or combine independent high-specificity evidence families. The detector must set authority explicitly after validating the product or protocol shape; naming an evidence method `descriptor` does not make it authoritative. A directory, history entry, filename, or process name alone must not become `confirmed`. Do not add shell-history inference.

Environment declarations are key names only. Listener declarations identify an already-listening port; they do not initiate network traffic. Cache layouts select a small built-in parser and cannot provide executable logic.

Prefer durable ecosystem contracts over product-name lists:

- Parse both the established `mcpServers` shape and the current MCP `servers` shape, but require each child to contain a non-empty command, endpoint, or recognized transport.
- Treat a `SKILL.md` as a skill only when its frontmatter satisfies the open Agent Skills metadata rules. Linked skill directories are supported, bounded, and never recursively followed outside the descriptor.
- Treat `.agent.md` and known user-agent-root documents as agent definitions only when they contain a valid, non-empty definition. `AGENTS.md`, `CLAUDE.md`, and similar files are repository instructions, not autonomous agents.
- Validate A2A Agent Cards by shape and support both the current `supportedInterfaces` endpoints and the earlier top-level URL.
- Match container images and packages on identifier boundaries. Substrings such as `notollama` or `langchain-helper` must not match `ollama` or `langchain`.
- Scope dependencies and source imports to their ecosystem with `language_packages` and `language_imports`; for example, Python's unrelated `ai` package must not identify the JavaScript Vercel AI SDK. For npm, Lens parses dependency collections rather than matching package names in keywords, descriptions, or funding URLs.
- A framework dependency or import proves framework use by a repository; it does not prove that an autonomous agent exists.
- Inspect IDE extension manifests for durable AI contribution points. A recognized extension ID maps to its stable product runtime; an unknown extension with chat-participant, language-model-tool, or MCP-provider contributions enters capability inventory as an IDE tool. It is not promoted to an autonomous agent or root system until stronger product or definition evidence exists.

Product IDs should remain narrow and stable. Distinct products such as Gemini CLI and Antigravity must not share one runtime identity merely because they share configuration ancestry. New market entrants can be added through versioned, checksummed packs, while protocol and artifact parsers remain built into Lens so packs cannot execute code.
