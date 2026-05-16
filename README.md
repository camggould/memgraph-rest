# memgraph-rest

REST + SSE API server for [memgraph](https://github.com/camggould/memgraph),
the agentic persistence layer.

memgraph-rest imports memgraph as a library and exposes the `Store` over
HTTP under `/v1`. Writes can be streamed to subscribers over Server-Sent
Events at `/v1/stream`. It is intentionally thin: no UI, no auth model
beyond a single shared bearer token (provide your own access layer in
production).

A static viewer will be embedded in a future release; the embed seam is
already wired and serves a placeholder page from `viewer/static/`.

See:

- [memgraph PRD](https://github.com/camggould/memgraph/blob/main/PRD.md) — substrate design.
- [DESIGN.md](./DESIGN.md) — endpoint reference, auth, SSE shape, viewer plan.

## Install

```bash
go install github.com/camggould/memgraph-rest/cmd/memgraph-rest@latest
```

Or build from source:

```bash
git clone https://github.com/camggould/memgraph-rest
cd memgraph-rest
go build -o memgraph-rest ./cmd/memgraph-rest
```

> TODO: one-liner curl install lands in a follow-up dist phase.

## Run

```bash
memgraph-rest serve --sqlite memgraph.db --addr :8080
```

With auth (set the env var):

```bash
MEMGRAPH_HTTP_TOKEN=secret memgraph-rest serve
curl -H "Authorization: Bearer secret" http://localhost:8080/v1/graphs
```

With CORS allow-listing:

```bash
memgraph-rest serve --cors-origin http://localhost:3000 --cors-origin https://my.app
```

With Postgres:

```bash
memgraph-rest serve --postgres "postgres://user:pass@host/db?sslmode=disable"
```

## Endpoints

See [DESIGN.md](./DESIGN.md) for the full surface. Highlights:

- `GET /v1/graphs`, `POST /v1/graphs`, `GET /v1/graphs/:id`, `PATCH /v1/graphs/:id`
- `POST /v1/nodes`, `GET /v1/nodes/:lineage_id`, `GET /v1/nodes/:lineage_id/history`
- `GET /v1/nodes/:lineage_id/neighborhood`
- `POST /v1/edges`, `DELETE /v1/edges/:id`
- `GET /v1/graphs/:id/search?q=...`
- `GET /v1/stream` (SSE)
- `GET /healthz`, `GET /v1/info`

## License

MIT.
