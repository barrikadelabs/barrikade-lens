# Evidence-backed capability topology

Lens snapshot 1.3 records the observational meaning of every relationship. In
addition to confidence and evidence references, an edge carries its source
surface, observation time, and one of these states:

- `declared`: a bounded configuration or protocol descriptor states the edge;
- `discovered`: sanitized artifact metadata supports an inferred edge; or
- `observed`: a live endpoint or Kubernetes observation supports the edge.

These states describe evidence, not effective authorization. Lens never treats
an observed or declared capability as proof that a call is permitted.

## MCP topology

MCP client configuration is reduced to server name, transport, sanitized URL,
enabled state, environment-key names, credential presence, and bounded declared
tool names. Tool descriptions, input schemas, instructions, commands, arguments,
headers, credentials, and environment values are not retained.

When direct evidence exists, collectors emit:

`agent/runtime → MCP server → declared tool`

and, for a remote endpoint:

`MCP server → sanitized API/service destination`

Remote MCP identity is based on its sanitized endpoint, so endpoint, repository,
and Kubernetes observations converge without display-name matching. Local stdio
servers remain target-scoped because a name alone is not a safe cross-surface
identity. API descriptions converge with MCP destinations only through a stable
sanitized host key. Repository agents connect to an MCP server only when their
descriptor explicitly names it.

Snapshot 1.1 and 1.2 remain accepted during rollout. The Hub records their
relationships as `discovered` on the snapshot's source surface.

## Metadata handshake boundary

Static descriptor discovery is enabled. An active MCP metadata handshake remains
off unless a future deployment explicitly opts in. Any such implementation must
be bounded to protocol initialization and metadata listing, use a dedicated
timeout and response-size limit, and must never call a tool. The current
collector does not open a connection to an MCP server during discovery.
