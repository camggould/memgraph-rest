// Package memgraphrest exposes a memgraph.Store over HTTP and SSE.
//
// The package is transport-only: it does not own the store and does not
// authenticate writers. Pair it with the SQLite or Postgres store
// implementations shipped in github.com/camggould/memgraph.
package memgraphrest
