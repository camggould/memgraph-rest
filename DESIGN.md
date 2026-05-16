# memgraph-rest design

A thin HTTP+SSE transport over `memgraph.Store`. Stdlib net/http only; no
routing framework.

## Endpoint reference

All paths are versioned under `/v1`. JSON in, JSON out. Status codes:
200 read, 201 create, 204 delete, 400 invalid input, 401 auth, 404 not
found, 409 manual-conflict, 500 internal, 503 store unavailable.

### Graphs

| Method | Path                              | Body / Query                              | Response                |
| ------ | --------------------------------- | ----------------------------------------- | ----------------------- |
| GET    | `/v1/graphs`                      | —                                         | `{ graphs: [GraphOut] }` |
| POST   | `/v1/graphs`                      | `CreateGraphIn`                           | `GraphOut` (201)        |
| GET    | `/v1/graphs/:id`                  | —                                         | `GraphOut`              |
| PATCH  | `/v1/graphs/:id`                  | `UpdateGraphIn`                           | `GraphOut`              |
| GET    | `/v1/graphs/:id/symlinks`         | —                                         | `SymlinkManifestOut`    |
| GET    | `/v1/graphs/:id/nodes`            | `kinds,tags,limit,offset`                 | `{ nodes, next_offset }`|
| GET    | `/v1/graphs/:id/search`           | `q,kinds,tags,fresh_only,limit`           | `{ hits: [SearchHitOut] }` |

`GraphOut` carries a `symlink_manifest_summary: { outbound_count, inbound_count }`.

### Nodes

| Method | Path                                       | Body / Query                          | Response               |
| ------ | ------------------------------------------ | ------------------------------------- | ---------------------- |
| POST   | `/v1/nodes`                                | `PutNodeIn`                           | `NodeOut` (201) or `ConflictOut` (409) |
| GET    | `/v1/nodes/:lineage_id`                    | `version, at` (RFC3339)               | `NodeOut`              |
| GET    | `/v1/nodes/:lineage_id/history`            | —                                     | `{ versions: [NodeOut] }` newest first |
| GET    | `/v1/nodes/:lineage_id/outgoing`           | `kinds`                               | `{ edges: [EdgeOut] }` |
| GET    | `/v1/nodes/:lineage_id/incoming`           | `kinds`                               | `{ edges: [EdgeOut] }` |
| GET    | `/v1/nodes/:lineage_id/neighborhood`       | `depth, kinds, follow_symlinks, max_nodes` | `{ nodes, edges }` |

`NodeOut` includes computed `is_current` (no superseded_by) and `is_stale`
(`is_current` AND `freshness_at` < now). Conflicts surface as a list of
lineage_id strings.

### Edges

| Method | Path                | Body          | Response          |
| ------ | ------------------- | ------------- | ----------------- |
| POST   | `/v1/edges`         | `PutEdgeIn`   | `EdgeOut` (201)   |
| DELETE | `/v1/edges/:id`     | —             | 204 No Content    |

### Stream

`GET /v1/stream` (Accept: text/event-stream) subscribes to the store via
`Store.Subscribe`. Events:

- `event: node.written\ndata: <NodeOut>`
- `event: edge.written\ndata: <EdgeOut>`
- `event: graph.created\ndata: <GraphOut>`
- `event: ping\ndata: {}` every 30s for keepalive

The server registers exactly one upstream subscription on first SSE client
and fans out to per-client buffered channels (64). Slow consumers drop
events rather than block the store callback.

### Health

- `GET /healthz` → plaintext `ok`
- `GET /v1/info` → `{ version, time, store }`

### Viewer

- `GET /` → serves `viewer/static/index.html` (embedded).
- `GET /assets/*` → embedded static files via `http.FileServer(http.FS(viewer.FS))`.

## Auth model

Single shared bearer token via the `MEMGRAPH_HTTP_TOKEN` env var (read by
the CLI; library callers use `WithToken`). When set, every request except
`/healthz` requires `Authorization: Bearer <token>`. Comparison uses
`crypto/subtle.ConstantTimeCompare` to make timing attacks against the
token impractical. Missing or wrong token returns HTTP 401 with
`{"error":"unauthorized"}`.

The shared-token model is **deliberately minimal**. memgraph itself takes
no position on identity; wrap memgraph-rest in your own access layer in
production (reverse-proxy auth, API gateway, etc.).

## Manual-conflict behavior

`POST /v1/nodes` with `based_on_version` set may produce a manual conflict
when the graph's `conflict_policy=manual`. The underlying store still
writes the node as a sibling head. The HTTP layer translates
`memgraph.ErrConflictManual` to **HTTP 409** with body:

```json
{
  "error": "memgraph: concurrent write under manual conflict policy; conflict recorded",
  "node":  { ...NodeOut for the just-written sibling... },
  "conflicts": ["<lineage_id of sibling>", ...]
}
```

Clients resolve by writing a third version that explicitly supersedes both
siblings.

## Middleware

Applied outer-to-inner: recovery → logging → auth → cors → mux.

- **recovery** logs the panic and stack and returns 500 JSON.
- **logging** emits one line per request: `method=X path=Y status=N duration=...`.
- **auth** short-circuits 401 if a token is configured and the header
  doesn't match. Skipped for `/healthz`.
- **cors** is a no-op when `WithCORS` is unset; otherwise it allow-lists
  the configured origins and handles OPTIONS preflight.

## Viewer embed plan

`viewer/embed.go` declares `//go:embed static/*`. The current static
content is a single placeholder `index.html`. When the SPA viewer ships,
its build output drops into `viewer/static/` and is automatically picked
up by the existing embed and the `/` + `/assets/*` routes. No handler
changes required.

## Storage adapters

The library never imports a specific store; callers pass any
`memgraph.Store`. The CLI ships two backends:

- `--sqlite <path>` (default `memgraph.db`) → `store/sqlite.Open`
- `--postgres <DSN>` → `store/postgres.OpenContext`

The `/v1/info` response labels the store ("sqlite" or "postgres") so
clients can adjust their expectations.
