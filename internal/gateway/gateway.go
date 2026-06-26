package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Server struct {
	cfg      config.Config
	agent    *agent.Agent
	registry *tools.Registry
	mux      *http.ServeMux
	sessions map[string]*Session
	mu       sync.Mutex
}

type Session struct {
	ID       string             `json:"id"`
	Created  time.Time          `json:"created"`
	Messages []protocol.Message `json:"-"`
	mu       sync.Mutex
}

type RunRequest struct {
	Prompt          string `json:"prompt"`
	Model           string `json:"model,omitempty"`
	Thinking        string `json:"thinking,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	MaxToolRounds   int    `json:"max_tool_rounds,omitempty"`
}

func NewServer(cfg config.Config, prov provider.Provider, registry *tools.Registry) *Server {
	s := &Server{
		cfg:      cfg,
		agent:    agent.New(cfg, prov, registry),
		registry: registry,
		mux:      http.NewServeMux(),
		sessions: map[string]*Session{},
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 1)
	go func() {
		errs <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /v1/tools", s.handleTools)
	s.mux.HandleFunc("POST /v1/run", s.handleRun)
	s.mux.HandleFunc("POST /v1/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /v1/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("POST /v1/sessions/{id}/run", s.handleSessionRun)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"provider": s.cfg.Provider,
		"model":    s.cfg.Model,
	})
}

func (s *Server) handleTools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.registry.Specs())
}

func (s *Server) handleCreateSession(w http.ResponseWriter, _ *http.Request) {
	session := &Session{
		ID:       newID(),
		Created:  time.Now().UTC(),
		Messages: agent.InitialMessages(),
	}
	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	session.mu.Lock()
	count := len(session.Messages)
	session.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            session.ID,
		"created":       session.Created,
		"message_count": count,
	})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "prompt required")
		return
	}
	streamEvents(w, func(emit func(protocol.Event)) error {
		a, err := s.agentFor(req)
		if err != nil {
			return err
		}
		return a.Run(r.Context(), req.Prompt, emit)
	})
}

func (s *Server) handleSessionRun(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "prompt required")
		return
	}
	session.mu.Lock()
	session.Messages = append(session.Messages, protocol.Message{Role: protocol.RoleUser, Content: req.Prompt})
	streamEvents(w, func(emit func(protocol.Event)) error {
		a, err := s.agentFor(req)
		if err != nil {
			return err
		}
		next, err := a.RunMessages(r.Context(), session.Messages, emit)
		if err == nil {
			session.Messages = next
		}
		return err
	})
	session.mu.Unlock()
}

func (s *Server) session(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *Server) agentFor(req RunRequest) (*agent.Agent, error) {
	cfg := s.cfg
	if req.Model != "" {
		cfg.Model = req.Model
	}
	if req.Thinking != "" {
		cfg.Thinking = req.Thinking
	}
	if req.ReasoningEffort != "" {
		cfg.ReasoningEffort = req.ReasoningEffort
	}
	if req.MaxToolRounds > 0 {
		cfg.MaxToolRounds = req.MaxToolRounds
	}
	prov, err := provider.New(cfg)
	if err != nil {
		return nil, err
	}
	registry := tools.NewRegistry(cfg)
	return agent.New(cfg, prov, registry), nil
}

func streamEvents(w http.ResponseWriter, run func(func(protocol.Event)) error) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	emit := func(event protocol.Event) {
		_ = enc.Encode(event)
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := run(emit); err != nil {
		emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}
