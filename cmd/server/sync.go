package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// hub fans record changes out to Server-Sent Events subscribers, per sync key.
// It is deliberately lossy: a subscriber that cannot keep up gets its event
// dropped and recovers with a normal incremental pull.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[chan []record]struct{}
}

func newHub() *hub {
	return &hub{subs: map[string]map[chan []record]struct{}{}}
}

func (h *hub) subscribe(key string) chan []record {
	ch := make(chan []record, 64)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[key] == nil {
		h.subs[key] = map[chan []record]struct{}{}
	}
	h.subs[key][ch] = struct{}{}
	return ch
}

func (h *hub) unsubscribe(key string, ch chan []record) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs := h.subs[key]; subs != nil {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(h.subs, key)
		}
	}
}

func (h *hub) publish(key string, recs []record) {
	if len(recs) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[key] {
		select {
		case ch <- recs:
		default: // slow subscriber: drop, it will re-pull
		}
	}
}

// syncPull returns every record touched after ?since=, i.e. an incremental
// delta. Tombstones are included so deletions propagate too.
func (s *server) syncPull(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRE.MatchString(key) {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	if err := s.ensureImported(key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	recs, err := s.store.delta(key, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	max, err := s.store.maxUpdated(key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"records": recs, "serverTime": max})
}

// syncPush merges a client's changed records with last-write-wins and streams
// whatever actually changed to every SSE subscriber (the pusher included).
func (s *server) syncPush(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRE.MatchString(key) {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBlobBytes+(1<<20)))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	var payload struct {
		Records []record `json:"records"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.ensureImported(key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	changed, err := s.store.upsert(key, payload.Records)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.hub.publish(key, changed)
	w.WriteHeader(http.StatusNoContent)
}

// events is the Server-Sent Events stream: it sends a `records` event whenever
// another client (or the MCP) changes something for this key.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRE.MatchString(key) {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	// Subscribe before announcing readiness, so a client that reacts to the
	// first byte cannot miss an event.
	ch := s.hub.subscribe(key)
	defer s.hub.unsubscribe(key, ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // no proxy buffering (reverse proxy)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case recs := <-ch:
			data, err := json.Marshal(map[string]any{"records": recs})
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: records\ndata: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

// broadcast is a nil-safe helper for handlers that mutate the store.
func (s *server) broadcast(key string, recs []record) {
	if s.hub != nil {
		s.hub.publish(key, recs)
	}
}
