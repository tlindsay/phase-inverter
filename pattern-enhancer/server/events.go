package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
)

// handleEvents streams state to one subscriber as Server-Sent Events.
//
// A full snapshot goes out on connect, before any update. That is what makes
// reconnection trivially correct: a client that slept through ten changes does
// not need to replay them, it just needs the truth now.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	updates := s.subscribe()
	defer s.unsubscribe(updates)

	if !writeSSE(w, flusher, s.backend.State()) {
		return
	}

	for {
		select {
		case st := <-updates:
			if !writeSSE(w, flusher, st) {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, f http.Flusher, st pe.State) bool {
	payload, err := json.Marshal(st)
	if err != nil {
		return false
	}
	// id carries Seq so a client can order this against a command response
	// without decoding the body first.
	if _, err := fmt.Fprintf(w, "id: %d\nevent: state\ndata: %s\n\n", st.Seq, payload); err != nil {
		return false
	}
	f.Flush()
	return true
}

// Publish fans a new state out to every subscriber.
//
// Sends are non-blocking with latest-wins replacement: state is a snapshot, so
// a slow consumer wants the newest value, never a backlog of stale ones. This
// also means one wedged client can never stall the daemon.
func (s *Server) Publish(st pe.State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for ch := range s.subs {
		select {
		case ch <- st:
		default:
			// Subscriber has not drained the previous value. Drop it and
			// leave the newer one in its place.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- st:
			default:
			}
		}
	}
}

func (s *Server) subscribe() chan pe.State {
	ch := make(chan pe.State, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *Server) unsubscribe(ch chan pe.State) {
	s.mu.Lock()
	delete(s.subs, ch)
	s.mu.Unlock()
}
