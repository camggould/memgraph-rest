package memgraphrest_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	memgraph "github.com/camggould/memgraph"
	"github.com/camggould/memgraph/store/sqlite"
	memgraphrest "github.com/camggould/memgraph-rest"
)

// --- harness ---

type harness struct {
	t      *testing.T
	store  *sqlite.Store
	server *memgraphrest.Server
	ts     *httptest.Server
	token  string
}

func newHarness(t *testing.T, opts ...memgraphrest.Option) *harness {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	srv := memgraphrest.New(store, opts...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Close()
		_ = store.Close()
	})
	return &harness{t: t, store: store, server: srv, ts: ts}
}

func (h *harness) req(method, path string, body any) *http.Request {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.ts.URL+path, rdr)
	if err != nil {
		h.t.Fatalf("new req: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	return req
}

func (h *harness) do(req *http.Request) (*http.Response, []byte) {
	h.t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	return resp, b
}

func (h *harness) jsonDo(method, path string, body any, into any) (status int) {
	h.t.Helper()
	resp, raw := h.do(h.req(method, path, body))
	if into != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, into); err != nil {
			h.t.Fatalf("unmarshal %s %s status=%d body=%s: %v", method, path, resp.StatusCode, string(raw), err)
		}
	}
	return resp.StatusCode
}

func mustStatus(t *testing.T, got, want int, body []byte) {
	t.Helper()
	if got != want {
		t.Fatalf("status got=%d want=%d body=%s", got, want, string(body))
	}
}

// --- tests ---

func TestHealthAndInfo(t *testing.T) {
	h := newHarness(t, memgraphrest.WithVersion("test-1"), memgraphrest.WithStoreKind("sqlite"))

	resp, body := h.do(h.req("GET", "/healthz", nil))
	mustStatus(t, resp.StatusCode, 200, body)
	if string(body) != "ok" {
		t.Fatalf("healthz body=%q", string(body))
	}

	var info memgraphrest.InfoOut
	if got := h.jsonDo("GET", "/v1/info", nil, &info); got != 200 {
		t.Fatalf("info status=%d", got)
	}
	if info.Version != "test-1" || info.Store != "sqlite" {
		t.Fatalf("info=%+v", info)
	}
}

func TestGraphCRUD(t *testing.T) {
	h := newHarness(t)
	var g memgraphrest.GraphOut
	if got := h.jsonDo("POST", "/v1/graphs", memgraphrest.CreateGraphIn{Name: "primary"}, &g); got != 201 {
		t.Fatalf("create status=%d", got)
	}
	if g.ID == "" || g.Name != "primary" {
		t.Fatalf("graph=%+v", g)
	}

	var list memgraphrest.GraphsListOut
	if got := h.jsonDo("GET", "/v1/graphs", nil, &list); got != 200 {
		t.Fatalf("list status=%d", got)
	}
	if len(list.Graphs) != 1 || list.Graphs[0].ID != g.ID {
		t.Fatalf("list=%+v", list)
	}

	var got memgraphrest.GraphOut
	if status := h.jsonDo("GET", "/v1/graphs/"+g.ID, nil, &got); status != 200 {
		t.Fatalf("get status=%d", status)
	}
	if got.ID != g.ID {
		t.Fatalf("get id mismatch")
	}
}

func TestNodeLifecycle(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")

	var v1 memgraphrest.NodeOut
	if s := h.jsonDo("POST", "/v1/nodes", memgraphrest.PutNodeIn{
		GraphID: g.ID,
		Kind:    "fact",
		Content: "hello world",
	}, &v1); s != 201 {
		t.Fatalf("v1 status=%d", s)
	}
	if v1.LineageID == "" || v1.Version != 1 {
		t.Fatalf("v1=%+v", v1)
	}

	var v2 memgraphrest.NodeOut
	if s := h.jsonDo("POST", "/v1/nodes", memgraphrest.PutNodeIn{
		GraphID:   g.ID,
		LineageID: v1.LineageID,
		Kind:      "fact",
		Content:   "hello again",
	}, &v2); s != 201 {
		t.Fatalf("v2 status=%d", s)
	}
	if v2.LineageID != v1.LineageID || v2.Version != 2 {
		t.Fatalf("v2=%+v", v2)
	}

	var current memgraphrest.NodeOut
	if s := h.jsonDo("GET", "/v1/nodes/"+v1.LineageID, nil, &current); s != 200 {
		t.Fatalf("current status=%d", s)
	}
	if current.Version != 2 {
		t.Fatalf("current.Version=%d", current.Version)
	}

	var hist memgraphrest.HistoryOut
	if s := h.jsonDo("GET", "/v1/nodes/"+v1.LineageID+"/history", nil, &hist); s != 200 {
		t.Fatalf("history status=%d", s)
	}
	if len(hist.Versions) != 2 || hist.Versions[0].Version != 2 {
		t.Fatalf("history=%+v", hist)
	}

	var pinned memgraphrest.NodeOut
	if s := h.jsonDo("GET", "/v1/nodes/"+v1.LineageID+"?version=1", nil, &pinned); s != 200 {
		t.Fatalf("pinned status=%d", s)
	}
	if pinned.Version != 1 {
		t.Fatalf("pinned version=%d", pinned.Version)
	}
}

func TestEdgesAndNeighborhood(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")
	a := mustPutNode(t, h, g.ID, "fact", "a", "")
	b := mustPutNode(t, h, g.ID, "fact", "b", "")

	var edge memgraphrest.EdgeOut
	if s := h.jsonDo("POST", "/v1/edges", memgraphrest.PutEdgeIn{
		GraphID:     g.ID,
		FromLineage: a.LineageID,
		ToLineage:   b.LineageID,
		Kind:        "cites",
	}, &edge); s != 201 {
		t.Fatalf("edge status=%d", s)
	}
	if edge.ID == "" {
		t.Fatalf("edge id empty")
	}

	var out memgraphrest.EdgesListOut
	if s := h.jsonDo("GET", "/v1/nodes/"+a.LineageID+"/outgoing", nil, &out); s != 200 {
		t.Fatalf("outgoing status=%d", s)
	}
	if len(out.Edges) != 1 || out.Edges[0].ID != edge.ID {
		t.Fatalf("outgoing=%+v", out)
	}

	var nb memgraphrest.NeighborhoodOut
	if s := h.jsonDo("GET", "/v1/nodes/"+a.LineageID+"/neighborhood?depth=2", nil, &nb); s != 200 {
		t.Fatalf("nb status=%d", s)
	}
	if len(nb.Nodes) < 2 || len(nb.Edges) < 1 {
		t.Fatalf("nb=%+v", nb)
	}
}

func TestNeighborhood_DirectionOutgoing(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")
	a := mustPutNode(t, h, g.ID, "fact", "a", "")
	b := mustPutNode(t, h, g.ID, "fact", "b", "")
	c := mustPutNode(t, h, g.ID, "fact", "c", "")
	mustPutEdge(t, h, g.ID, a.LineageID, b.LineageID, "cites")
	mustPutEdge(t, h, g.ID, b.LineageID, c.LineageID, "cites")

	var nb memgraphrest.NeighborhoodOut
	if s := h.jsonDo("GET", "/v1/nodes/"+a.LineageID+"/neighborhood?depth=2&direction=outgoing", nil, &nb); s != 200 {
		t.Fatalf("status=%d", s)
	}
	if !containsLineage(nb.Nodes, b.LineageID) || !containsLineage(nb.Nodes, c.LineageID) {
		t.Fatalf("outgoing should reach b and c; got=%+v", nb.Nodes)
	}
}

func TestNeighborhood_DirectionIncoming(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")
	a := mustPutNode(t, h, g.ID, "fact", "a", "")
	b := mustPutNode(t, h, g.ID, "fact", "b", "")
	c := mustPutNode(t, h, g.ID, "fact", "c", "")
	mustPutEdge(t, h, g.ID, a.LineageID, b.LineageID, "cites")
	mustPutEdge(t, h, g.ID, b.LineageID, c.LineageID, "cites")

	var nb memgraphrest.NeighborhoodOut
	if s := h.jsonDo("GET", "/v1/nodes/"+c.LineageID+"/neighborhood?depth=2&direction=incoming", nil, &nb); s != 200 {
		t.Fatalf("status=%d", s)
	}
	if !containsLineage(nb.Nodes, b.LineageID) || !containsLineage(nb.Nodes, a.LineageID) {
		t.Fatalf("incoming should reach b and a from c; got=%+v", nb.Nodes)
	}
}

func TestNeighborhood_DirectionBoth(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")
	a := mustPutNode(t, h, g.ID, "fact", "a", "")
	b := mustPutNode(t, h, g.ID, "fact", "b", "")
	c := mustPutNode(t, h, g.ID, "fact", "c", "")
	mustPutEdge(t, h, g.ID, a.LineageID, b.LineageID, "cites")
	mustPutEdge(t, h, g.ID, b.LineageID, c.LineageID, "cites")

	var nb memgraphrest.NeighborhoodOut
	if s := h.jsonDo("GET", "/v1/nodes/"+b.LineageID+"/neighborhood?depth=1&direction=both", nil, &nb); s != 200 {
		t.Fatalf("status=%d", s)
	}
	if !containsLineage(nb.Nodes, a.LineageID) || !containsLineage(nb.Nodes, c.LineageID) {
		t.Fatalf("both should reach a and c from b; got=%+v", nb.Nodes)
	}
}

func TestNeighborhood_DirectionInvalid(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")
	a := mustPutNode(t, h, g.ID, "fact", "a", "")

	resp, body := h.do(h.req("GET", "/v1/nodes/"+a.LineageID+"/neighborhood?direction=sideways", nil))
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	var e memgraphrest.ErrorOut
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.ErrorCode != "invalid_direction" {
		t.Fatalf("error_code=%q body=%s", e.ErrorCode, string(body))
	}
}

func TestSearch(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")
	mustPutNode(t, h, g.ID, "fact", "pineapple grows on a bush", "")
	mustPutNode(t, h, g.ID, "fact", "oranges grow on trees", "")

	var out memgraphrest.SearchOut
	if s := h.jsonDo("GET", "/v1/graphs/"+g.ID+"/search?q=pineapple", nil, &out); s != 200 {
		t.Fatalf("search status=%d", s)
	}
	if len(out.Hits) == 0 || !strings.Contains(out.Hits[0].Node.Content, "pineapple") {
		t.Fatalf("hits=%+v", out)
	}
}

func TestDescribeSchemaAndListTags(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")

	put := func(kind string, tags []string) {
		var n memgraphrest.NodeOut
		if s := h.jsonDo("POST", "/v1/nodes", memgraphrest.PutNodeIn{
			GraphID: g.ID, Kind: kind, Content: "x", Tags: tags,
		}, &n); s != 201 {
			t.Fatalf("put node status=%d", s)
		}
	}
	put("recipe", []string{"protein:beef", "cuisine:french"})
	put("recipe", []string{"protein:chicken", "cuisine:french"})
	put("preference", []string{"weeknight"})

	var schema memgraphrest.SchemaDescriptionOut
	if s := h.jsonDo("GET", "/v1/graphs/"+g.ID+"/schema", nil, &schema); s != 200 {
		t.Fatalf("schema status=%d", s)
	}
	if schema.NodeCount != 3 || len(schema.Kinds) != 2 {
		t.Fatalf("schema=%+v", schema)
	}
	foundProtein := false
	for _, p := range schema.TagPrefixes {
		if p.Prefix == "protein" {
			foundProtein = true
		}
	}
	if !foundProtein {
		t.Fatalf("missing protein prefix in %+v", schema.TagPrefixes)
	}

	var tags memgraphrest.TagsListOut
	if s := h.jsonDo("GET", "/v1/graphs/"+g.ID+"/tags?prefix=protein:", nil, &tags); s != 200 {
		t.Fatalf("tags status=%d", s)
	}
	if len(tags.Tags) != 2 {
		t.Fatalf("tags=%+v", tags)
	}
}

func TestSymlinks(t *testing.T) {
	h := newHarness(t)
	g1 := mustCreateGraph(t, h, "g1", "")
	g2 := mustCreateGraph(t, h, "g2", "")
	a := mustPutNode(t, h, g1.ID, "fact", "a", "")
	b := mustPutNode(t, h, g2.ID, "fact", "b", "")

	if s := h.jsonDo("POST", "/v1/edges", memgraphrest.PutEdgeIn{
		GraphID:     g1.ID,
		FromLineage: a.LineageID,
		ToGraph:     g2.ID,
		ToLineage:   b.LineageID,
		Kind:        "ref",
	}, nil); s != 201 {
		t.Fatalf("edge status=%d", s)
	}

	var m memgraphrest.SymlinkManifestOut
	if s := h.jsonDo("GET", "/v1/graphs/"+g1.ID+"/symlinks", nil, &m); s != 200 {
		t.Fatalf("manifest status=%d", s)
	}
	if len(m.Outbound) != 1 || m.Outbound[0].GraphID != g2.ID {
		t.Fatalf("manifest=%+v", m)
	}
}

func TestManualConflict(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "manual")
	first := mustPutNode(t, h, g.ID, "fact", "v1", "")

	v := first.Version
	// Two concurrent writes both based on version=1.
	resp1, body1 := h.do(h.req("POST", "/v1/nodes", memgraphrest.PutNodeIn{
		GraphID:        g.ID,
		LineageID:      first.LineageID,
		Kind:           "fact",
		Content:        "fork-a",
		BasedOnVersion: &v,
	}))
	mustStatus(t, resp1.StatusCode, 201, body1)

	resp2, body2 := h.do(h.req("POST", "/v1/nodes", memgraphrest.PutNodeIn{
		GraphID:        g.ID,
		LineageID:      first.LineageID,
		Kind:           "fact",
		Content:        "fork-b",
		BasedOnVersion: &v,
	}))
	if resp2.StatusCode != 409 {
		t.Fatalf("expected 409, got %d body=%s", resp2.StatusCode, string(body2))
	}
	var conflict memgraphrest.ConflictOut
	if err := json.Unmarshal(body2, &conflict); err != nil {
		t.Fatalf("unmarshal conflict: %v", err)
	}
	if conflict.Node.LineageID != first.LineageID || len(conflict.Conflicts) == 0 {
		t.Fatalf("conflict=%+v", conflict)
	}
}

func TestAuth(t *testing.T) {
	h := newHarness(t, memgraphrest.WithToken("secret"))

	// no auth header → 401
	resp, body := h.do(h.req("GET", "/v1/graphs", nil))
	mustStatus(t, resp.StatusCode, 401, body)

	// healthz works without auth
	rh, bh := h.do(h.req("GET", "/healthz", nil))
	mustStatus(t, rh.StatusCode, 200, bh)

	// with auth → 200
	h.token = "secret"
	r2, b2 := h.do(h.req("GET", "/v1/graphs", nil))
	mustStatus(t, r2.StatusCode, 200, b2)

	// bad token → 401
	h.token = "wrong"
	r3, b3 := h.do(h.req("GET", "/v1/graphs", nil))
	mustStatus(t, r3.StatusCode, 401, b3)
}

func TestSSE(t *testing.T) {
	h := newHarness(t)

	// Open the SSE stream first so the server's subscription is active before
	// we write.
	req, _ := http.NewRequest("GET", h.ts.URL+"/v1/stream", nil)
	req.Header.Set("Accept", "text/event-stream")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 {
		t.Fatalf("stream status=%d", resp.StatusCode)
	}

	events := make(chan string, 8)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				select {
				case events <- strings.TrimPrefix(line, "event: "):
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Small pause so the goroutine is scanning before the write fires.
	time.Sleep(50 * time.Millisecond)

	if s := h.jsonDo("POST", "/v1/graphs", memgraphrest.CreateGraphIn{Name: "stream-test"}, nil); s != 201 {
		t.Fatalf("create status=%d", s)
	}

	select {
	case ev := <-events:
		if ev != "graph.created" {
			t.Fatalf("first event=%q want graph.created", ev)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("no SSE event within 1s")
	}
	cancel()
	wg.Wait()
}

func TestViewerPlaceholder(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(h.req("GET", "/", nil))
	mustStatus(t, resp.StatusCode, 200, body)
	if !strings.Contains(string(body), "<title>memgraph viewer</title>") {
		t.Fatalf("body missing title: %s", string(body))
	}
}

// --- helpers ---

func mustCreateGraph(t *testing.T, h *harness, name, policy string) memgraphrest.GraphOut {
	t.Helper()
	var g memgraphrest.GraphOut
	in := memgraphrest.CreateGraphIn{Name: name, ConflictPolicy: policy}
	if s := h.jsonDo("POST", "/v1/graphs", in, &g); s != 201 {
		t.Fatalf("create graph %s status=%d", name, s)
	}
	return g
}

func mustPutNode(t *testing.T, h *harness, graphID, kind, content, lineage string) memgraphrest.NodeOut {
	t.Helper()
	var n memgraphrest.NodeOut
	if s := h.jsonDo("POST", "/v1/nodes", memgraphrest.PutNodeIn{
		GraphID:   graphID,
		Kind:      kind,
		Content:   content,
		LineageID: lineage,
	}, &n); s != 201 {
		t.Fatalf("put node status=%d", s)
	}
	return n
}

func mustPutEdge(t *testing.T, h *harness, graphID, from, to, kind string) memgraphrest.EdgeOut {
	t.Helper()
	var e memgraphrest.EdgeOut
	if s := h.jsonDo("POST", "/v1/edges", memgraphrest.PutEdgeIn{
		GraphID:     graphID,
		FromLineage: from,
		ToLineage:   to,
		Kind:        kind,
	}, &e); s != 201 {
		t.Fatalf("put edge status=%d", s)
	}
	return e
}

func containsLineage(ns []memgraphrest.NodeOut, lineage string) bool {
	for _, n := range ns {
		if n.LineageID == lineage {
			return true
		}
	}
	return false
}

// Suppress "unused import" while keeping memgraph available for future tests.
var _ = memgraph.ErrNotFound

// Avoid an "imported and not used" warning if we ever stub.
var _ = fmt.Sprintf

func TestListNodes_Compact(t *testing.T) {
	h := newHarness(t)
	g := mustCreateGraph(t, h, "g1", "")

	var v1 memgraphrest.NodeOut
	if s := h.jsonDo("POST", "/v1/nodes", memgraphrest.PutNodeIn{
		GraphID:  g.ID,
		Kind:     "fact",
		Content:  "heavy payload that compact should omit",
		Summary:  "canvas-label",
		Tags:     []string{"a"},
		Metadata: map[string]any{"k": "v"},
	}, &v1); s != 201 {
		t.Fatalf("put status=%d", s)
	}

	// Full mode: heavy fields present in parsed struct and in raw JSON.
	resp, rawFull := h.do(h.req("GET", "/v1/graphs/"+g.ID+"/nodes", nil))
	mustStatus(t, resp.StatusCode, 200, rawFull)
	if !strings.Contains(string(rawFull), `"content":"heavy`) {
		t.Fatalf("full mode: content missing from raw JSON: %s", rawFull)
	}
	if !strings.Contains(string(rawFull), `"metadata"`) {
		t.Fatalf("full mode: metadata missing from raw JSON: %s", rawFull)
	}

	// Compact mode: heavy field keys absent from raw JSON.
	respC, rawCompact := h.do(h.req("GET", "/v1/graphs/"+g.ID+"/nodes?compact=1", nil))
	mustStatus(t, respC.StatusCode, 200, rawCompact)
	if strings.Contains(string(rawCompact), `"content"`) {
		t.Fatalf("compact mode: content present in raw JSON: %s", rawCompact)
	}
	if strings.Contains(string(rawCompact), `"metadata"`) {
		t.Fatalf("compact mode: metadata present in raw JSON: %s", rawCompact)
	}
	if strings.Contains(string(rawCompact), `"freshness_at"`) {
		t.Fatalf("compact mode: freshness_at present in raw JSON: %s", rawCompact)
	}
	if strings.Contains(string(rawCompact), `"created_by"`) {
		t.Fatalf("compact mode: created_by present in raw JSON: %s", rawCompact)
	}
	// Light fields still present.
	if !strings.Contains(string(rawCompact), `"summary":"canvas-label"`) {
		t.Fatalf("compact mode: summary missing: %s", rawCompact)
	}
	if !strings.Contains(string(rawCompact), `"lineage_id":"`+v1.LineageID+`"`) {
		t.Fatalf("compact mode: lineage_id missing: %s", rawCompact)
	}
}
