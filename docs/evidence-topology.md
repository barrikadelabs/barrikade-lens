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

Static descriptor discovery remains the default. A local scan can explicitly
opt in to a remote Streamable HTTP MCP metadata exchange with
`--probe-mcp-url https://server.example/mcp --allow-probe-host server.example`.
The probe sends only `initialize`, `notifications/initialized`, and up to four
pages of `tools/list`. It supports JSON and SSE responses, limits the entire
exchange to five seconds, each response to 1 MiB by default (2 MiB hard
maximum), and the result to 100 safe
tool names. It never invokes a tool or retains server instructions, tool
descriptions, schemas, arguments, session identifiers, or response bodies.
Unallowlisted hosts, credential-bearing or parameterized URLs, and cloud
metadata addresses are rejected. Probe failures produce a partial scan with a
generic diagnostic. Stateful SSE-only servers using the older HTTP+SSE
transport and servers requiring authentication are not yet probed.

The observed server and tool IDs use the same sanitized endpoint key as static
declarations, so live metadata enriches an existing server instead of creating
a display-name match. The resulting `provides` edge is `observed`; it says the
server listed the tool, not that a caller can execute it.

The Evidence Graph has a bounded path query in both directions. Selecting a
system shows downstream paths; selecting a connected resource shows which
current paths lead to it. The API returns at most 80 paths of three hops, with
at most 12 next edges explored per node. Every returned edge has a current,
unexpired evidence reference from a non-revoked source, confidence, surface,
observation state, and observation time. These paths are technical evidence,
not an effective authorization result.
