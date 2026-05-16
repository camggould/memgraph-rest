---
name: memgraph-rest
description: Use this skill when interacting with a memgraph-rest HTTP server — the REST + SSE transport layer over memgraph. Covers how to discover graphs, read and write nodes and edges over /v1/* JSON endpoints, traverse neighborhoods, search, subscribe to live updates via Server-Sent Events, handle bearer-token auth, and interpret the manual-conflict 409 response. Invoke whenever an agent has HTTP fetch tooling and a memgraph-rest base URL is known.
---

# memgraph-rest — HTTP API guide

memgraph-rest exposes a memgraph deployment over HTTP. It's the right transport when MCP isn't available — building integrations, embedding in a web app, debugging from `curl`, or implementing a non-MCP client.

If you have `memgraph_*` MCP tools available, prefer those — they're more discoverable. Use this skill when you only have HTTP capability.

## Base URL and auth

- Default base URL is whatever the server reports — typically `http://localhost:8080` for local installs or `/v1` on a same-origin deploy.
- If the server is started with `MEMGRAPH_HTTP_TOKEN` set, every request needs `Authorization: Bearer <token>`. `/healthz` is the only path that bypasses auth.
- A 401 means missing/wrong token. A 403 means the server rejected the auth handshake — same fix, just check the token.

## Read endpoints

All return JSON. Status 200 unless noted.

| Method | Path | Use |
|---|---|---|
| GET | `/v1/graphs` | List all graphs in the deployment |
| GET | `/v1/graphs/{id}` | One graph with symlink summary |
| GET | `/v1/graphs/{id}/symlinks` | Full inbound/outbound manifest |
| GET | `/v1/graphs/{id}/nodes?kinds=&tags=&limit=&offset=` | Paginated node list |
| GET | `/v1/graphs/{id}/search?q=&kinds=&tags=&fresh_only=&limit=` | Ranked full-text search |
| GET | `/v1/nodes/{lineage_id}` | Current version |
| GET | `/v1/nodes/{lineage_id}?version=N` | Specific historical version |
| GET | `/v1/nodes/{lineage_id}?at=<RFC3339>` | Point-in-time read |
| GET | `/v1/nodes/{lineage_id}/history` | All versions, newest first |
| GET | `/v1/nodes/{lineage_id}/outgoing?kinds=` | Edges out (parent → children, citations, etc.) |
| GET | `/v1/nodes/{lineage_id}/incoming?kinds=` | Edges in (backlinks) |
| GET | `/v1/nodes/{lineage_id}/neighborhood?depth=2&max_nodes=50&follow_symlinks=false&kinds=` | BFS expansion returning `{nodes, edges}` |

All `kinds` and `tags` parameters are repeatable: `?tags=a&tags=b` is AND-semantics on tags, OR on kinds.

## Write endpoints

| Method | Path | Use | Returns |
|---|---|---|---|
| POST | `/v1/graphs` | Create a graph | 201 + GraphOut |
| PATCH | `/v1/graphs/{id}` | Update graph config | 200 + GraphOut |
| POST | `/v1/nodes` | Create lineage or append version | 201 + NodeOut (or 409 — see below) |
| POST | `/v1/edges` | Create an edge (intra-graph or symlink) | 201 + EdgeOut |
| DELETE | `/v1/edges/{id}` | Remove an edge | 204 |

### Creating a node

POST body:

```json
{
  "graph_id": "...",
  "kind": "fact",
  "content": "JWT tokens expire after 1 hour by default.",
  "summary": "JWT expiration default",
  "tags": ["auth", "jwt"],
  "metadata": {"source_url": "..."},
  "freshness_at": "2026-12-31T00:00:00Z",
  "created_by": "agent:claude",
  "lineage_id": "...",
  "based_on_version": 3
}
```

- Omit `lineage_id` to create a new lineage; supply it to append a new version.
- `based_on_version` is optimistic concurrency. If supplied and the current version is newer, behavior depends on the graph's `conflict_policy`.

### Manual conflict — HTTP 409

When the graph's `conflict_policy=manual` and your `based_on_version` is behind, the server **still writes the node** (as a sibling head) and returns **HTTP 409**:

```json
{
  "error": "concurrent write under manual conflict policy",
  "node": { /* the just-written NodeOut */ },
  "conflicts": ["lineage_id_of_other_head", "..."]
}
```

Resolution = a third POST that explicitly supersedes both siblings. Read the current `conflicts` list, then write a new version with the same `lineage_id` and content that reconciles them.

## SSE: live updates

`GET /v1/stream` opens an `text/event-stream`. Events:

- `node.written` — payload is a `NodeOut`
- `edge.written` — payload is an `EdgeOut`
- `graph.created` — payload is a `GraphOut`
- `ping` — empty payload every 30s

If you're using fetch + streaming, parse line-by-line: `event: <name>\ndata: <json>\n\n`. `EventSource` works too but can't send `Authorization` headers — use fetch streaming when auth is enabled.

Reconnect on errors with backoff (start at 1s, cap at 30s).

## Health / info

- `GET /healthz` → plaintext `ok` — auth-free, use for liveness checks
- `GET /v1/info` → `{version, time, store: "sqlite"|"postgres"}` — useful to discover backend type

## Common workflows

### Discover and read

```
1. GET /v1/info                            # confirm server type
2. GET /v1/graphs                          # find a graph
3. GET /v1/graphs/{id}/search?q=...        # locate a node
4. GET /v1/nodes/{lineage}                 # full payload
5. GET /v1/nodes/{lineage}/neighborhood    # explore relationships
```

### Write a fact and link it

```
1. POST /v1/nodes
   body: { graph_id, kind: "fact", content: "...", tags: [...], created_by: "agent:claude" }
   → take the response's lineage_id

2. POST /v1/edges
   body: { graph_id, from_lineage: <new>, to_lineage: <existing related>, kind: "cites", created_by: "agent:claude" }
```

### Subscribe to changes

```
GET /v1/stream
   parse events; route node.written / edge.written / graph.created to your UI or downstream pipe
```

### Optimistic update

```
1. GET /v1/nodes/{lineage}                  → note the `version`
2. POST /v1/nodes
   body: { graph_id, lineage_id, content: "...", based_on_version: <noted> }
   → 201 on clean win; 409 + conflicts list if manual policy and someone raced
```

## Response shapes

`NodeOut` and `EdgeOut` mirror memgraph's MCP DTOs. Notable fields:

- `is_current` — true if this is the head of its lineage
- `is_stale` — true if `freshness_at` is past and `is_current`
- `superseded_by` — non-null only when this is a historical version
- `conflicts` — list of sibling lineage_ids; non-empty only under manual policy with active concurrent heads

For full schemas, see `dto.go` in the memgraph-rest repo.

## Anti-patterns

- **Don't poll `/v1/nodes/.../history` to detect changes.** Use `/v1/stream`.
- **Don't pass `based_on_version` if you didn't fetch first.** It's an optimistic-concurrency hint, not a creation marker.
- **Don't ignore the 409 body.** Read `conflicts` and resolve, or you'll keep creating sibling heads forever.
- **Don't put `Authorization` in URL params.** Header only.
- **Don't traverse via repeated GETs when you want a neighborhood.** Use `/v1/nodes/{id}/neighborhood?depth=N` — it bounds itself and returns nodes + edges in one shot.

## When NOT to use this skill

- When `memgraph_*` MCP tools are available — they have richer client-side handling and the same semantics. Use them instead.
- For document-shaped writes — `docs_*` MCP tools (or the memgraph-docs library) wrap the structural conventions.
