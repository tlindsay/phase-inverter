// Package server exposes one receiver over HTTP: REST for commands, SSE for
// state push.
//
// The split is deliberate. Commands travel on ordinary stateless requests, so
// they keep working when the event stream is down — a laptop that just woke or
// changed networks can still turn the volume down while its stream is still
// reconnecting. A single bidirectional socket would have taken commands down
// with it at exactly that moment.
//
// What that costs is ordering: a command's response and the stream event
// describing the same change arrive independently. Every State carries a Seq
// for precisely this, and clients keep the higher one.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
)

// Backend is the device-facing half of the daemon, as the HTTP layer needs it.
type Backend interface {
	State() pe.State
	AdjustVolume(ctx context.Context, steps int) error
	SetInput(ctx context.Context, input string) error
	SetPower(ctx context.Context, on bool) error
	TogglePower(ctx context.Context) error
	SetMute(ctx context.Context, on bool) error
	ToggleMute(ctx context.Context) error
}

// Server routes HTTP for a single named device and fans state out to SSE
// subscribers.
type Server struct {
	deviceID string
	backend  Backend

	// StatsFunc, when set, contributes daemon health counters to /v1/healthz.
	// A hook rather than a Backend method so the transport layer does not need
	// to know the shape of whatever the device chooses to report.
	StatsFunc func() any

	mu   sync.Mutex
	subs map[chan pe.State]struct{}
}

func New(deviceID string, b Backend) *Server {
	return &Server{
		deviceID: deviceID,
		backend:  b,
		subs:     make(map[chan pe.State]struct{}),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/devices/{id}/state", s.withDevice(s.handleState))
	mux.HandleFunc("GET /v1/devices/{id}/events", s.withDevice(s.handleEvents))
	mux.HandleFunc("POST /v1/devices/{id}/volume", s.withDevice(s.handleVolume))
	mux.HandleFunc("POST /v1/devices/{id}/mute", s.withDevice(s.handleMute))
	mux.HandleFunc("POST /v1/devices/{id}/input", s.withDevice(s.handleInput))
	mux.HandleFunc("POST /v1/devices/{id}/power", s.withDevice(s.handlePower))
	return mux
}

// withDevice rejects requests for a device this daemon does not serve, so a
// typo produces a 404 rather than silently driving the wrong receiver.
func (s *Server) withDevice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != s.deviceID {
			http.Error(w, "unknown device", http.StatusNotFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	st := s.backend.State()

	body := map[string]any{
		"ok":     true,
		"device": s.deviceID,
		"epoch":  st.Epoch,
		"seq":    st.Seq,
		"online": st.Online,
	}

	// Health is mostly about the parts that fail quietly. A rising poll count
	// beside a frozen event count means the UDP subscription has lapsed —
	// state is still correct, but it is no longer live.
	if s.StatsFunc != nil {
		body["stats"] = s.StatsFunc()
	}

	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.backend.State())
}

// command payloads. Pointers distinguish "absent" from "false", which matters
// for mute: {"on":false} and {} are different requests.
type volumeCmd struct {
	Step     *int `json:"step"`
	Absolute *int `json:"absolute"`
}

type toggleCmd struct {
	Toggle bool  `json:"toggle"`
	On     *bool `json:"on"`
}

type inputCmd struct {
	Input string `json:"input"`
}

func (s *Server) handleVolume(w http.ResponseWriter, r *http.Request) {
	var cmd volumeCmd
	if !decodeBody(w, r, &cmd) {
		return
	}
	if cmd.Step == nil {
		http.Error(w, "step is required", http.StatusBadRequest)
		return
	}
	s.runCommand(w, r, func() error {
		return s.backend.AdjustVolume(r.Context(), *cmd.Step)
	})
}

func (s *Server) handleMute(w http.ResponseWriter, r *http.Request) {
	var cmd toggleCmd
	if !decodeBody(w, r, &cmd) {
		return
	}
	s.runCommand(w, r, func() error {
		if cmd.On != nil {
			return s.backend.SetMute(r.Context(), *cmd.On)
		}
		return s.backend.ToggleMute(r.Context())
	})
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	var cmd toggleCmd
	if !decodeBody(w, r, &cmd) {
		return
	}
	s.runCommand(w, r, func() error {
		// An explicit value wins; otherwise the device flips its own state.
		// Computing the inverse here would reintroduce the stale-cache bug
		// that relative commands exist to avoid.
		if cmd.On != nil {
			return s.backend.SetPower(r.Context(), *cmd.On)
		}
		return s.backend.TogglePower(r.Context())
	})
}

func (s *Server) handleInput(w http.ResponseWriter, r *http.Request) {
	var cmd inputCmd
	if !decodeBody(w, r, &cmd) {
		return
	}
	if cmd.Input == "" {
		http.Error(w, "input is required", http.StatusBadRequest)
		return
	}
	s.runCommand(w, r, func() error {
		return s.backend.SetInput(r.Context(), cmd.Input)
	})
}

// runCommand executes a device command and replies with authoritative state.
//
// The response body is the point: a client never has to infer success from a
// later broadcast, which is what made the old MQTT path impossible to trust.
func (s *Server) runCommand(w http.ResponseWriter, _ *http.Request, do func() error) {
	if err := do(); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": err.Error(),
			"state": s.backend.State(),
		})
		return
	}
	writeJSON(w, http.StatusOK, s.backend.State())
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		http.Error(w, "malformed JSON body", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
