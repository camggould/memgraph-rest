package memgraphrest

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	memgraph "github.com/camggould/memgraph"
	"github.com/camggould/memgraph-rest/viewer"
)

// viewerSubFS strips the leading "static/" directory from the embed.FS so
// the file server can resolve requests like /assets/index-XYZ.js against
// the right path inside the embed.
var viewerSubFS = func() fs.FS {
	sub, err := fs.Sub(viewer.FS, "static")
	if err != nil {
		// embed.FS is built at compile time; if "static" isn't there the
		// binary is malformed — fatal at build, not at runtime, in
		// practice. Fall back to the raw FS so we at least serve /.
		return viewer.FS
	}
	return sub
}()

func (s *Server) routes(mux *http.ServeMux) {
	// Health + info
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /v1/info", s.handleInfo)

	// Graphs
	mux.HandleFunc("GET /v1/graphs", s.handleListGraphs)
	mux.HandleFunc("POST /v1/graphs", s.handleCreateGraph)
	mux.HandleFunc("GET /v1/graphs/{id}", s.handleGetGraph)
	mux.HandleFunc("PATCH /v1/graphs/{id}", s.handleUpdateGraph)
	mux.HandleFunc("GET /v1/graphs/{id}/symlinks", s.handleSymlinks)
	mux.HandleFunc("GET /v1/graphs/{id}/nodes", s.handleListNodes)
	mux.HandleFunc("GET /v1/graphs/{id}/search", s.handleSearch)

	// Nodes
	mux.HandleFunc("POST /v1/nodes", s.handlePutNode)
	mux.HandleFunc("GET /v1/nodes/{lineage}", s.handleGetNode)
	mux.HandleFunc("GET /v1/nodes/{lineage}/history", s.handleHistory)
	mux.HandleFunc("GET /v1/nodes/{lineage}/outgoing", s.handleOutgoing)
	mux.HandleFunc("GET /v1/nodes/{lineage}/incoming", s.handleIncoming)
	mux.HandleFunc("GET /v1/nodes/{lineage}/neighborhood", s.handleNeighborhood)

	// Edges
	mux.HandleFunc("POST /v1/edges", s.handlePutEdge)
	mux.HandleFunc("DELETE /v1/edges/{id}", s.handleDeleteEdge)

	// SSE
	mux.HandleFunc("GET /v1/stream", s.handleStream)

	// Viewer (root index + assets). Anything else falls through to 404.
	mux.HandleFunc("GET /", s.handleViewerRoot)
	mux.Handle("GET /assets/", http.FileServer(http.FS(viewerSubFS)))
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorOut{Error: msg})
}

func mapStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, memgraph.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, memgraph.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, memgraph.ErrKindNotAllowed):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, memgraph.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

// --- health + info ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, InfoOut{
		Version: s.version,
		Time:    time.Now().UTC(),
		Store:   s.storeKind,
	})
}

// --- graphs ---

func (s *Server) graphOutForID(r *http.Request, id memgraph.GraphID) (GraphOut, error) {
	g, err := s.store.GetGraph(r.Context(), id)
	if err != nil {
		return GraphOut{}, err
	}
	m, err := s.store.SymlinkManifest(r.Context(), id)
	if err != nil {
		return GraphOut{}, err
	}
	return toGraphOut(g, len(m.Outbound), len(m.Inbound)), nil
}

func (s *Server) handleListGraphs(w http.ResponseWriter, r *http.Request) {
	gs, err := s.store.ListGraphs(r.Context())
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := GraphsListOut{Graphs: make([]GraphOut, 0, len(gs))}
	for _, g := range gs {
		m, err := s.store.SymlinkManifest(r.Context(), g.ID)
		if err != nil {
			mapStoreError(w, err)
			return
		}
		out.Graphs = append(out.Graphs, toGraphOut(g, len(m.Outbound), len(m.Inbound)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	id := memgraph.GraphID(r.PathValue("id"))
	out, err := s.graphOutForID(r, id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateGraph(w http.ResponseWriter, r *http.Request) {
	var in CreateGraphIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	g, err := s.store.CreateGraph(r.Context(), memgraph.GraphInput{
		Name:           in.Name,
		ConflictPolicy: memgraph.ConflictPolicy(in.ConflictPolicy),
		KindWhitelist:  in.KindWhitelist,
		Metadata:       in.Metadata,
	})
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toGraphOut(g, 0, 0))
}

func (s *Server) handleUpdateGraph(w http.ResponseWriter, r *http.Request) {
	id := memgraph.GraphID(r.PathValue("id"))
	var in UpdateGraphIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	patch := memgraph.GraphConfigPatch{
		Name:          in.Name,
		KindWhitelist: in.KindWhitelist,
		Metadata:      in.Metadata,
	}
	if in.ConflictPolicy != nil {
		cp := memgraph.ConflictPolicy(*in.ConflictPolicy)
		patch.ConflictPolicy = &cp
	}
	if _, err := s.store.UpdateGraphConfig(r.Context(), id, patch); err != nil {
		mapStoreError(w, err)
		return
	}
	out, err := s.graphOutForID(r, id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSymlinks(w http.ResponseWriter, r *http.Request) {
	id := memgraph.GraphID(r.PathValue("id"))
	m, err := s.store.SymlinkManifest(r.Context(), id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSymlinkManifestOut(m))
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	id := memgraph.GraphID(r.PathValue("id"))
	q := r.URL.Query()
	filter := memgraph.NodeFilter{
		Kinds:  splitCSV(q.Get("kinds")),
		Tags:   splitCSV(q.Get("tags")),
		Limit:  parseIntDefault(q.Get("limit"), 50),
		Offset: parseIntDefault(q.Get("offset"), 0),
	}
	ns, err := s.store.ListNodes(r.Context(), id, filter)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := NodesListOut{Nodes: make([]NodeOut, 0, len(ns)), NextOffset: filter.Offset + len(ns)}
	for _, n := range ns {
		out.Nodes = append(out.Nodes, toNodeOut(n))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	id := memgraph.GraphID(r.PathValue("id"))
	q := r.URL.Query()
	query := memgraph.SearchQuery{
		Text:      q.Get("q"),
		Kinds:     splitCSV(q.Get("kinds")),
		Tags:      splitCSV(q.Get("tags")),
		FreshOnly: parseBool(q.Get("fresh_only")),
		Limit:     parseIntDefault(q.Get("limit"), 20),
	}
	hits, err := s.store.Search(r.Context(), id, query)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := SearchOut{Hits: make([]SearchHitOut, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, SearchHitOut{
			Node:    toNodeOut(h.Node),
			Snippet: h.Snippet,
			Score:   h.Score,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- nodes ---

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	lineage := memgraph.LineageID(r.PathValue("lineage"))
	q := r.URL.Query()
	var opts memgraph.ReadOpts
	if v := q.Get("version"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "version must be int")
			return
		}
		opts.AtVersion = &n
	}
	if t := q.Get("at"); t != "" {
		ts, err := time.Parse(time.RFC3339, t)
		if err != nil {
			writeError(w, http.StatusBadRequest, "at must be RFC3339")
			return
		}
		opts.AtTime = &ts
	}
	n, err := s.store.GetNodeByLineage(r.Context(), lineage, opts)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toNodeOut(n))
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	lineage := memgraph.LineageID(r.PathValue("lineage"))
	ns, err := s.store.History(r.Context(), lineage)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := HistoryOut{Versions: make([]NodeOut, 0, len(ns))}
	for _, n := range ns {
		out.Versions = append(out.Versions, toNodeOut(n))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleOutgoing(w http.ResponseWriter, r *http.Request) {
	lineage := memgraph.LineageID(r.PathValue("lineage"))
	opts := memgraph.TraverseOpts{EdgeKinds: splitCSV(r.URL.Query().Get("kinds"))}
	es, err := s.store.Outgoing(r.Context(), lineage, opts)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := EdgesListOut{Edges: make([]EdgeOut, 0, len(es))}
	for _, e := range es {
		out.Edges = append(out.Edges, toEdgeOut(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleIncoming(w http.ResponseWriter, r *http.Request) {
	lineage := memgraph.LineageID(r.PathValue("lineage"))
	opts := memgraph.TraverseOpts{EdgeKinds: splitCSV(r.URL.Query().Get("kinds"))}
	es, err := s.store.Incoming(r.Context(), lineage, opts)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := EdgesListOut{Edges: make([]EdgeOut, 0, len(es))}
	for _, e := range es {
		out.Edges = append(out.Edges, toEdgeOut(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleNeighborhood(w http.ResponseWriter, r *http.Request) {
	lineage := memgraph.LineageID(r.PathValue("lineage"))
	q := r.URL.Query()
	opts := memgraph.TraverseOpts{
		MaxDepth:       parseIntDefault(q.Get("depth"), 2),
		EdgeKinds:      splitCSV(q.Get("kinds")),
		FollowSymlinks: parseBool(q.Get("follow_symlinks")),
		MaxNodes:       parseIntDefault(q.Get("max_nodes"), 50),
	}
	res, err := s.store.Traverse(r.Context(), lineage, opts)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := NeighborhoodOut{
		Nodes: make([]NodeOut, 0, len(res.Nodes)),
		Edges: make([]EdgeOut, 0, len(res.Edges)),
	}
	for _, n := range res.Nodes {
		out.Nodes = append(out.Nodes, toNodeOut(n))
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, toEdgeOut(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutNode(w http.ResponseWriter, r *http.Request) {
	var in PutNodeIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if in.GraphID == "" {
		writeError(w, http.StatusBadRequest, "graph_id required")
		return
	}
	var fresh *time.Time
	if in.FreshnessAt != "" {
		t, err := time.Parse(time.RFC3339, in.FreshnessAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "freshness_at must be RFC3339")
			return
		}
		fresh = &t
	}
	createdBy := in.CreatedBy
	if createdBy == "" {
		createdBy = "unknown"
	}
	n, err := s.store.PutNode(r.Context(), memgraph.NodeInput{
		GraphID:        memgraph.GraphID(in.GraphID),
		LineageID:      memgraph.LineageID(in.LineageID),
		Kind:           in.Kind,
		Content:        in.Content,
		Summary:        in.Summary,
		Tags:           in.Tags,
		Metadata:       in.Metadata,
		FreshnessAt:    fresh,
		CreatedBy:      createdBy,
		BasedOnVersion: in.BasedOnVersion,
	})
	if err != nil {
		// Manual-conflict: the write succeeded as a sibling head. Return 409
		// with the node and conflicts list so the client can resolve.
		if errors.Is(err, memgraph.ErrConflictManual) {
			no := toNodeOut(n)
			writeJSON(w, http.StatusConflict, ConflictOut{
				Error:     err.Error(),
				Node:      no,
				Conflicts: no.Conflicts,
			})
			return
		}
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toNodeOut(n))
}

// --- edges ---

func (s *Server) handlePutEdge(w http.ResponseWriter, r *http.Request) {
	var in PutEdgeIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if in.GraphID == "" || in.FromLineage == "" || in.ToLineage == "" || in.Kind == "" {
		writeError(w, http.StatusBadRequest, "graph_id, from_lineage, to_lineage, kind required")
		return
	}
	createdBy := in.CreatedBy
	if createdBy == "" {
		createdBy = "unknown"
	}
	e, err := s.store.PutEdge(r.Context(), memgraph.EdgeInput{
		GraphID:     memgraph.GraphID(in.GraphID),
		FromLineage: memgraph.LineageID(in.FromLineage),
		ToGraph:     memgraph.GraphID(in.ToGraph),
		ToLineage:   memgraph.LineageID(in.ToLineage),
		Kind:        in.Kind,
		Metadata:    in.Metadata,
		Ordinal:     in.Ordinal,
		CreatedBy:   createdBy,
	})
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toEdgeOut(e))
}

func (s *Server) handleDeleteEdge(w http.ResponseWriter, r *http.Request) {
	id := memgraph.EdgeID(r.PathValue("id"))
	if err := s.store.DeleteEdge(r.Context(), id); err != nil {
		mapStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- viewer ---

func (s *Server) handleViewerRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := viewer.FS.ReadFile("static/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "viewer not embedded")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
