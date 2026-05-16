package memgraphrest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	memgraph "github.com/camggould/memgraph"
)

// sseClient is a single SSE subscriber.
type sseClient struct {
	events chan sseEvent
	done   chan struct{}
}

type sseEvent struct {
	name string
	data any
}

// hubHandler implements memgraph.WriteHandler and fans out to clients.
type hubHandler struct {
	s *Server
}

func (h hubHandler) OnNodeWritten(_ context.Context, n memgraph.Node) {
	h.s.broadcast(sseEvent{name: "node.written", data: toNodeOut(n)})
}

func (h hubHandler) OnEdgeWritten(_ context.Context, e memgraph.Edge) {
	h.s.broadcast(sseEvent{name: "edge.written", data: toEdgeOut(e)})
}

func (h hubHandler) OnGraphCreated(_ context.Context, g memgraph.Graph) {
	h.s.broadcast(sseEvent{name: "graph.created", data: toGraphOut(g, 0, 0)})
}

func (s *Server) broadcast(ev sseEvent) {
	s.subsMu.Lock()
	clients := make([]*sseClient, 0, len(s.sseSubs))
	for c := range s.sseSubs {
		clients = append(clients, c)
	}
	s.subsMu.Unlock()
	for _, c := range clients {
		select {
		case c.events <- ev:
		case <-c.done:
		default:
			// Drop slow consumers rather than block the store callback.
		}
	}
}

// ensureSubscription registers a single WriteHandler with the store the
// first time an SSE client connects. The subscription persists for the
// lifetime of the Server.
func (s *Server) ensureSubscription() error {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if s.sseUnsub != nil || s.sseStartErr != nil {
		return s.sseStartErr
	}
	unsub, err := s.store.Subscribe(hubHandler{s: s})
	if err != nil {
		s.sseStartErr = err
		return err
	}
	s.sseUnsub = unsub
	return nil
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	if err := s.ensureSubscription(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "subscribe: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := &sseClient{
		events: make(chan sseEvent, 64),
		done:   make(chan struct{}),
	}
	s.subsMu.Lock()
	s.sseSubs[client] = struct{}{}
	s.subsMu.Unlock()

	defer func() {
		s.subsMu.Lock()
		if _, ok := s.sseSubs[client]; ok {
			delete(s.sseSubs, client)
		}
		s.subsMu.Unlock()
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.done:
			return
		case <-ping.C:
			if err := writeSSE(w, "ping", struct{}{}); err != nil {
				return
			}
			flusher.Flush()
		case ev := <-client.events:
			if err := writeSSE(w, ev.name, ev.data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	return nil
}
