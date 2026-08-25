// Package dashboard implements the bd dashboard HTTP server, REST API, and
// Server-Sent Events hub for live browser-based mission control.
package dashboard

import (
	"fmt"
	"net/http"
	"sync"
)

// SSEEvent is a single Server-Sent Event.
type SSEEvent struct {
	// Type is the event: event type (e.g. "issue.created").
	Type string
	// Data is the JSON payload.
	Data string
}

// Hub fans out SSE events to all connected browsers.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan SSEEvent]struct{}
}

// NewHub creates an empty SSE hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

// subscribe registers a new client channel and returns it.
func (h *Hub) subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// unsubscribe removes a client channel from the hub.
func (h *Hub) unsubscribe(ch chan SSEEvent) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

// Broadcast sends an event to all connected clients.
func (h *Hub) Broadcast(event SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- event:
		default:
			// Drop if client is slow; it will get a full refresh on next reconnect.
		}
	}
}

// ServeHTTP implements the SSE endpoint (GET /api/v1/stream).
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := h.subscribe()
	defer h.unsubscribe(ch)

	// Send an initial heartbeat so the browser knows the stream is live.
	fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
			flusher.Flush()
		}
	}
}
