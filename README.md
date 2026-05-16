# memgraph-rest

REST + SSE API server for [memgraph](https://github.com/camggould/memgraph),
the agentic persistence layer.

memgraph-rest imports memgraph as a library and exposes the `Store` over
HTTP under `/v1`. Writes can be streamed to subscribers over Server-Sent
Events at `/v1/stream`. It is intentionally thin: no application logic,
no auth model beyond a single shared bearer token (provide your own
access layer in production).

The binary embeds the [memgraph-viewer](https://github.com/camggould/memgraph-viewer)
SPA so `GET /` serves the full UI out of the box.

See:

- [memgraph PRD](https://github.com/camggould/memgraph/blob/main/PRD.md) — substrate design.
- [DESIGN.md](./DESIGN.md) — endpoint reference, auth, SSE shape, viewer embed.
- [SKILL.md](./SKILL.md) — agent-facing guide to the HTTP API.

## Install

### One-line install (macOS, Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/camggould/memgraph-rest/main/install.sh | sh
```

Downloads the latest release from GitHub, verifies SHA-256 against
`checksums.txt`, and installs to `/usr/local/bin` (or
`$HOME/.local/bin` when sudo isn't available).

Override defaults via env vars:

```sh
curl -fsSL https://raw.githubusercontent.com/camggould/memgraph-rest/main/install.sh | MEMGRAPH_REST_VERSION=v0.1.0 sh
curl -fsSL https://raw.githubusercontent.com/camggould/memgraph-rest/main/install.sh | MEMGRAPH_REST_INSTALL_DIR=$HOME/bin sh
```

### Pre-built binaries (manual)

Tarballs and `checksums.txt` on the
[Releases page](https://github.com/camggould/memgraph-rest/releases)
for darwin/linux on amd64+arm64 and windows/amd64.

### `go install`

```sh
go install github.com/camggould/memgraph-rest/cmd/memgraph-rest@latest
```

### Build from source

```sh
git clone https://github.com/camggould/memgraph-rest
cd memgraph-rest
go build -o memgraph-rest ./cmd/memgraph-rest
```

Pure-Go build, no cgo. The viewer's pre-built bundle is checked in at
`viewer/static/` and embedded via Go's `embed.FS`.

### Refreshing the viewer

```sh
cd ../memgraph-viewer && npm install && npm run build
rm -rf ../memgraph-rest/viewer/static/*
cp -R dist/* ../memgraph-rest/viewer/static/
```

Then rebuild memgraph-rest. The next release will ship the updated viewer.

### Cutting a new release (maintainers)

```sh
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
GITHUB_TOKEN=$(gh auth token) goreleaser release --clean
```

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
