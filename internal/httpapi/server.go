// Package httpapi serves the dashboard, JSON API, and WebSocket endpoint.
package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"baccarat-live-simulator/internal/model"
	"baccarat-live-simulator/internal/redact"
	"baccarat-live-simulator/internal/state"
	"baccarat-live-simulator/internal/ws"
	"baccarat-live-simulator/web"
)

const requestTimeout = 5 * time.Second

// Server is the localhost HTTP server.
type Server struct {
	mgr    *state.Manager
	hub    *ws.Hub
	logger *slog.Logger
	http   *http.Server
}

// New binds the handler to addr. addr must already be a loopback address.
func New(addr string, mgr *state.Manager, hub *ws.Hub, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{mgr: mgr, hub: hub, logger: logger}
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	return s
}

// ListenAndServe starts the server. Write and read deadlines are left unset
// because a WebSocket is a long-lived connection on the same listener.
// JSON handlers use their own request timeouts.
func (s *Server) ListenAndServe() error {
	return s.http.ListenAndServe()
}

// Shutdown stops accepting connections and waits for handlers to return.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// Handler returns the router for tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/rounds", s.handleRounds)
	mux.HandleFunc("POST /api/simulation/start", s.handleStart)
	mux.HandleFunc("POST /api/simulation/pause", s.handlePause)
	mux.HandleFunc("POST /api/simulation/resume", s.handleResume)
	mux.HandleFunc("POST /api/simulation/reset", s.handleReset)
	mux.HandleFunc("POST /api/simulation/speed", s.handleSpeed)
	mux.HandleFunc("GET /ws", s.handleWS)
	fileServer := http.FileServer(http.FS(web.Files()))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}))
	return s.log(s.secure(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	body := map[string]string{
		"status":   "ok",
		"database": "ok",
		"version":  model.Version,
	}
	if err := s.mgr.Ping(ctx); err != nil {
		s.logger.Error("health", "err", redact.Error(err))
		body["status"] = "degraded"
		body["database"] = "unavailable"
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	env, err := s.mgr.Snapshot(ctx)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) handleRounds(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "limit must be an integer from 1 to 500")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	rounds, err := s.mgr.Rounds(ctx, limit)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if rounds == nil {
		rounds = []model.Round{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rounds": rounds})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var body speedBody
	if err := decodeOptional(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if body.SpeedMS != nil {
		if _, err := s.mgr.SetSpeed(ctx, *body.SpeedMS); err != nil {
			s.writeControlErr(w, err)
			return
		}
	}
	env, err := s.mgr.Start(ctx)
	if err != nil {
		s.writeControlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.control(w, r, s.mgr.Pause)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	s.control(w, r, s.mgr.Resume)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.control(w, r, s.mgr.Reset)
}

func (s *Server) handleSpeed(w http.ResponseWriter, r *http.Request) {
	var body speedBody
	if err := decodeOptional(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.SpeedMS == nil {
		writeErr(w, http.StatusBadRequest, "speedMs is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	env, err := s.mgr.SetSpeed(ctx, *body.SpeedMS)
	if err != nil {
		s.writeControlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	env, err := s.mgr.Snapshot(r.Context())
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(env)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.hub.Accept(w, r, payload)
}

func (s *Server) control(w http.ResponseWriter, r *http.Request, fn func(context.Context) (state.Envelope, error)) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	env, err := fn(ctx)
	if err != nil {
		s.writeControlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

type speedBody struct {
	SpeedMS *int `json:"speedMs"`
}

func (s *Server) writeControlErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrInvalidSpeed):
		writeErr(w, http.StatusBadRequest, "speedMs must be between 200 and 30000")
	case errors.Is(err, state.ErrPaused):
		writeErr(w, http.StatusConflict, "simulation is paused; use resume")
	case errors.Is(err, state.ErrNotRunning):
		writeErr(w, http.StatusConflict, "simulation is not running")
	case errors.Is(err, state.ErrNotPaused):
		writeErr(w, http.StatusConflict, "simulation is not paused")
	default:
		s.writeStoreErr(w, err)
	}
}

func (s *Server) writeStoreErr(w http.ResponseWriter, err error) {
	s.logger.Error("request failed", "err", redact.Error(err))
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > model.MaxRoundQuery {
		return 0, errors.New("invalid limit")
	}
	return n, nil
}

func decodeOptional(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return errors.New("content type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

func writeErr(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		if r.URL.Path == "/ws" {
			return
		}
		s.logger.Info("http", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack is not supported")
	}
	return h.Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
