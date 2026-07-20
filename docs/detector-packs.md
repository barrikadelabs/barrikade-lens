# Detector packs

Detector packs are schema-versioned YAML data. Schema v2 requires every runtime signature to declare one of `agent_tool`, `model_runtime`, `host_application`, `development_runtime`, or `unclassified`. They can declare paths, configuration formats, process names, container-image patterns, environment-key names, skill roots, local model-server ports, known listeners, local model-cache layouts, framework packages, and import names. They contain no executable expressions, scripts, or hooks.

Use `barrikade-lens --detector-pack ./pack.yaml` to load a local pack. Lens validates IDs, configuration formats and scopes, cache layouts, ports, duplicate signatures, schema version, and an optional structural SHA-256 checksum before scanning. Hub distribution can deliver the same bytes; the collector still performs local validation.

New high-confidence detectors should use authoritative descriptors or combine independent evidence families. A directory or process name alone must not become `confirmed`. Do not add shell-history inference.

Environment declarations are key names only. Listener declarations identify an already-listening port; they do not initiate network traffic. Cache layouts select a small built-in parser and cannot provide executable logic.
