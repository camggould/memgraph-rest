// Package viewer holds the embedded static viewer assets served by
// memgraph-rest. The directory contents are intentionally a placeholder
// for now; a future release replaces static/ with a built SPA.
package viewer

import "embed"

//go:embed static/*
var FS embed.FS
