package memgraphrest

import (
	"time"

	memgraph "github.com/camggould/memgraph"
)

// --- Output DTOs (JSON-serialized forms of memgraph types) ---

type NodeOut struct {
	ID           string         `json:"id"`
	GraphID      string         `json:"graph_id"`
	LineageID    string         `json:"lineage_id"`
	Version      int            `json:"version"`
	Kind         string         `json:"kind"`
	Content      string         `json:"content"`
	Summary      string         `json:"summary,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	FreshnessAt  *time.Time     `json:"freshness_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	CreatedBy    string         `json:"created_by"`
	SupersededBy *string        `json:"superseded_by,omitempty"`
	IsCurrent    bool           `json:"is_current"`
	IsStale      bool           `json:"is_stale"`
	Conflicts    []string       `json:"conflicts,omitempty"`
}

func toNodeOut(n memgraph.Node) NodeOut {
	out := NodeOut{
		ID:          string(n.ID),
		GraphID:     string(n.GraphID),
		LineageID:   string(n.LineageID),
		Version:     n.Version,
		Kind:        n.Kind,
		Content:     n.Content,
		Summary:     n.Summary,
		Tags:        n.Tags,
		Metadata:    n.Metadata,
		FreshnessAt: n.FreshnessAt,
		CreatedAt:   n.CreatedAt,
		CreatedBy:   n.CreatedBy,
	}
	if n.SupersededBy != nil {
		s := string(*n.SupersededBy)
		out.SupersededBy = &s
	}
	if len(n.Conflicts) > 0 {
		out.Conflicts = make([]string, 0, len(n.Conflicts))
		for _, c := range n.Conflicts {
			out.Conflicts = append(out.Conflicts, string(c))
		}
	}
	out.IsCurrent = n.SupersededBy == nil
	out.IsStale = out.IsCurrent && n.FreshnessAt != nil && n.FreshnessAt.Before(time.Now())
	return out
}

type EdgeOut struct {
	ID          string         `json:"id"`
	GraphID     string         `json:"graph_id"`
	FromLineage string         `json:"from_lineage"`
	ToGraph     string         `json:"to_graph"`
	ToLineage   string         `json:"to_lineage"`
	Kind        string         `json:"kind"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Ordinal     *int           `json:"ordinal,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	CreatedBy   string         `json:"created_by"`
}

func toEdgeOut(e memgraph.Edge) EdgeOut {
	return EdgeOut{
		ID:          string(e.ID),
		GraphID:     string(e.GraphID),
		FromLineage: string(e.FromLineage),
		ToGraph:     string(e.ToGraph),
		ToLineage:   string(e.ToLineage),
		Kind:        e.Kind,
		Metadata:    e.Metadata,
		Ordinal:     e.Ordinal,
		CreatedAt:   e.CreatedAt,
		CreatedBy:   e.CreatedBy,
	}
}

type SymlinkManifestSummary struct {
	OutboundCount int `json:"outbound_count"`
	InboundCount  int `json:"inbound_count"`
}

type GraphOut struct {
	ID                     string                 `json:"id"`
	Name                   string                 `json:"name"`
	ConflictPolicy         string                 `json:"conflict_policy"`
	KindWhitelist          []string               `json:"kind_whitelist,omitempty"`
	Metadata               map[string]any         `json:"metadata,omitempty"`
	CreatedAt              time.Time              `json:"created_at"`
	SymlinkManifestSummary SymlinkManifestSummary `json:"symlink_manifest_summary"`
}

func toGraphOut(g memgraph.Graph, outbound, inbound int) GraphOut {
	return GraphOut{
		ID:             string(g.ID),
		Name:           g.Name,
		ConflictPolicy: string(g.ConflictPolicy),
		KindWhitelist:  g.KindWhitelist,
		Metadata:       g.Metadata,
		CreatedAt:      g.CreatedAt,
		SymlinkManifestSummary: SymlinkManifestSummary{
			OutboundCount: outbound,
			InboundCount:  inbound,
		},
	}
}

type GraphRefOut struct {
	GraphID   string `json:"graph_id"`
	EdgeCount int    `json:"edge_count"`
}

type SymlinkManifestOut struct {
	Outbound []GraphRefOut `json:"outbound"`
	Inbound  []GraphRefOut `json:"inbound"`
}

func toSymlinkManifestOut(m memgraph.SymlinkManifest) SymlinkManifestOut {
	out := SymlinkManifestOut{
		Outbound: make([]GraphRefOut, 0, len(m.Outbound)),
		Inbound:  make([]GraphRefOut, 0, len(m.Inbound)),
	}
	for _, r := range m.Outbound {
		out.Outbound = append(out.Outbound, GraphRefOut{GraphID: string(r.GraphID), EdgeCount: r.EdgeCount})
	}
	for _, r := range m.Inbound {
		out.Inbound = append(out.Inbound, GraphRefOut{GraphID: string(r.GraphID), EdgeCount: r.EdgeCount})
	}
	return out
}

type SearchHitOut struct {
	Node    NodeOut `json:"node"`
	Snippet string  `json:"snippet,omitempty"`
	Score   float64 `json:"score"`
}

// --- Schema discovery ---

type KindFreqOut struct {
	Kind     string   `json:"kind"`
	Count    int      `json:"count"`
	Examples []string `json:"examples,omitempty"`
}

type TagPrefixFreqOut struct {
	Prefix string   `json:"prefix"`
	Count  int      `json:"count"`
	Values []string `json:"values"`
}

type TagFreqOut struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

type SchemaDescriptionOut struct {
	GraphID     string             `json:"graph_id"`
	NodeCount   int                `json:"node_count"`
	Kinds       []KindFreqOut      `json:"kinds"`
	TagPrefixes []TagPrefixFreqOut `json:"tag_prefixes"`
	Tags        []TagFreqOut       `json:"tags"`
}

func toSchemaDescriptionOut(s memgraph.SchemaDescription) SchemaDescriptionOut {
	out := SchemaDescriptionOut{
		GraphID:     string(s.GraphID),
		NodeCount:   s.NodeCount,
		Kinds:       make([]KindFreqOut, 0, len(s.Kinds)),
		TagPrefixes: make([]TagPrefixFreqOut, 0, len(s.TagPrefixes)),
		Tags:        make([]TagFreqOut, 0, len(s.Tags)),
	}
	for _, k := range s.Kinds {
		out.Kinds = append(out.Kinds, KindFreqOut{Kind: k.Kind, Count: k.Count, Examples: k.Examples})
	}
	for _, p := range s.TagPrefixes {
		out.TagPrefixes = append(out.TagPrefixes, TagPrefixFreqOut{Prefix: p.Prefix, Count: p.Count, Values: p.Values})
	}
	for _, t := range s.Tags {
		out.Tags = append(out.Tags, TagFreqOut{Tag: t.Tag, Count: t.Count})
	}
	return out
}

type TagsListOut struct {
	Tags []TagFreqOut `json:"tags"`
}

// --- Input DTOs ---

type CreateGraphIn struct {
	Name           string         `json:"name"`
	ConflictPolicy string         `json:"conflict_policy,omitempty"`
	KindWhitelist  []string       `json:"kind_whitelist,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type UpdateGraphIn struct {
	Name           *string         `json:"name,omitempty"`
	ConflictPolicy *string         `json:"conflict_policy,omitempty"`
	KindWhitelist  []string        `json:"kind_whitelist,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
}

type PutNodeIn struct {
	GraphID        string         `json:"graph_id"`
	Kind           string         `json:"kind"`
	Content        string         `json:"content"`
	LineageID      string         `json:"lineage_id,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	FreshnessAt    string         `json:"freshness_at,omitempty"`
	CreatedBy      string         `json:"created_by,omitempty"`
	BasedOnVersion *int           `json:"based_on_version,omitempty"`
}

type PutEdgeIn struct {
	GraphID     string         `json:"graph_id"`
	FromLineage string         `json:"from_lineage"`
	ToLineage   string         `json:"to_lineage"`
	Kind        string         `json:"kind"`
	ToGraph     string         `json:"to_graph,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Ordinal     *int           `json:"ordinal,omitempty"`
	CreatedBy   string         `json:"created_by,omitempty"`
}

// --- Collection wrappers ---

type GraphsListOut struct {
	Graphs []GraphOut `json:"graphs"`
}

type NodesListOut struct {
	Nodes      []NodeOut `json:"nodes"`
	NextOffset int       `json:"next_offset"`
}

type EdgesListOut struct {
	Edges []EdgeOut `json:"edges"`
}

type HistoryOut struct {
	Versions []NodeOut `json:"versions"`
}

type NeighborhoodOut struct {
	Nodes []NodeOut `json:"nodes"`
	Edges []EdgeOut `json:"edges"`
}

type SearchOut struct {
	Hits []SearchHitOut `json:"hits"`
}

type InfoOut struct {
	Version string    `json:"version"`
	Time    time.Time `json:"time"`
	Store   string    `json:"store"`
}

type ErrorOut struct {
	Error     string `json:"error"`
	ErrorCode string `json:"error_code,omitempty"`
}

// ConflictOut is the response body for HTTP 409 from POST /v1/nodes when the
// graph's conflict policy is "manual" and a concurrent write was recorded.
type ConflictOut struct {
	Error     string   `json:"error"`
	Node      NodeOut  `json:"node"`
	Conflicts []string `json:"conflicts"`
}
