package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Server struct {
	cfg       config.Config
	agent     *agent.Agent
	registry  *tools.Registry
	auth      credentials.Manager
	mux       *http.ServeMux
	authToken string
	sessions  map[string]*Session
	store     *sessionStore
	mu        sync.Mutex
}

type ServerOptions struct {
	AuthToken       string
	SessionStoreDir string
}

type Session struct {
	ID            string              `json:"id"`
	Created       time.Time           `json:"created"`
	Thread        *sessionpkg.Session `json:"-"`
	events        *eventHub
	eventRecorder func(protocol.Event)
	mu            sync.Mutex
	status        SessionStatus
}

type RunRequest struct {
	Prompt          string `json:"prompt"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	Profile         string `json:"profile,omitempty"`
	Thinking        string `json:"thinking,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	MaxToolRounds   int    `json:"max_tool_rounds,omitempty"`
}

type CreateSessionRequest struct {
	Messages []protocol.Message `json:"messages,omitempty"`
	Profile  string             `json:"profile,omitempty"`
}

type DeepSeekAuthRequest struct {
	APIKey string `json:"api_key"`
}

type CodexImportRequest struct {
	SourcePath string          `json:"source_path,omitempty"`
	AuthJSON   json.RawMessage `json:"auth_json,omitempty"`
}

func NewServer(cfg config.Config, prov provider.Provider, registry *tools.Registry) *Server {
	return NewServerWithOptions(cfg, prov, registry, ServerOptions{})
}

func NewServerWithOptions(cfg config.Config, prov provider.Provider, registry *tools.Registry, opts ServerOptions) *Server {
	s := &Server{
		cfg:      cfg,
		agent:    agent.New(cfg, prov, registry),
		registry: registry,
		auth:     credentials.NewManager(cfg),
		mux:      http.NewServeMux(),
		sessions: map[string]*Session{},
	}
	if strings.TrimSpace(opts.SessionStoreDir) != "" {
		s.store = newSessionStore(opts.SessionStoreDir)
		loaded, err := s.store.LoadAll()
		if err != nil {
			log.Printf("gateway session store load failed: %v", err)
		}
		for _, session := range loaded {
			s.attachSessionStore(session)
			s.sessions[session.ID] = session
		}
	}
	opts.AuthToken = strings.TrimSpace(opts.AuthToken)
	s.authToken = opts.AuthToken
	s.routes()
	return s
}

func DefaultSessionStoreDir() string {
	return filepath.Join(config.BillyHomeDir(), "gateway-sessions")
}

func (s *Server) Handler() http.Handler {
	if s.authToken != "" {
		return s.authMiddleware(s.mux)
	}
	return s.mux
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, listener)
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	server := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 1)
	go func() {
		errs <- server.Serve(listener)
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

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || isLoopbackRemoteAddr(r.RemoteAddr) || bearerTokenMatches(r.Header.Get("Authorization"), s.authToken) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="billyharness-gateway"`)
		writeError(w, http.StatusUnauthorized, "gateway bearer token required")
	})
}

func bearerTokenMatches(header, token string) bool {
	fields := strings.Fields(strings.TrimSpace(header))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(fields[1]), []byte(token)) == 1
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /v1/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("POST /v1/auth/deepseek", s.handleDeepSeekAuth)
	s.mux.HandleFunc("POST /v1/auth/codex/import", s.handleCodexImport)
	s.mux.HandleFunc("GET /v1/config", s.handleConfigStatus)
	s.mux.HandleFunc("GET /v1/tools", s.handleTools)
	s.mux.HandleFunc("GET /v1/mcp", s.handleMCP)
	s.mux.HandleFunc("POST /v1/run", s.handleRun)
	s.mux.HandleFunc("POST /v1/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /v1/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("GET /v1/sessions/{id}/status", s.handleSessionStatus)
	s.mux.HandleFunc("GET /v1/sessions/{id}/events", s.handleSessionEvents)
	s.mux.HandleFunc("POST /v1/sessions/{id}/run", s.handleSessionRun)
	s.mux.HandleFunc("POST /v1/sessions/{id}/cancel", s.handleSessionCancel)
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

func (s *Server) handleAuthStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.auth.Status())
}

func (s *Server) handleConfigStatus(w http.ResponseWriter, _ *http.Request) {
	base, err := config.Resolve()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resolved, err := config.Resolve(config.RuntimeDiffOverrides(base.Config, s.cfg, config.SourceGateway)...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":   resolved.SanitizedConfig(),
		"values":   resolved.SanitizedValues(),
		"warnings": resolved.Warnings,
	})
}

func (s *Server) handleDeepSeekAuth(w http.ResponseWriter, r *http.Request) {
	var req DeepSeekAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	status, err := s.auth.SaveDeepSeekAPIKey(req.APIKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deepseek": status,
	})
}

func (s *Server) handleCodexImport(w http.ResponseWriter, r *http.Request) {
	var req CodexImportRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	var (
		status credentials.ProviderStatus
		err    error
	)
	if len(req.AuthJSON) > 0 {
		status, err = s.auth.SaveCodexAuthJSON(req.AuthJSON)
	} else {
		status, err = s.auth.ImportCodexAuth(req.SourcePath)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"codex": status,
	})
}

func (s *Server) handleMCP(w http.ResponseWriter, _ *http.Request) {
	cfg := s.registry.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"config_files": cfg.MCPConfigFiles,
		"allowed":      cfg.MCPAllowedServers,
		"enabled":      cfg.MCPEnabled,
		"servers":      s.registry.MCPStatuses(),
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	messages := req.Messages
	if len(messages) == 0 {
		cfg := s.cfg
		if strings.TrimSpace(req.Profile) != "" {
			cfg.Profile = config.NormalizeProfileName(req.Profile)
		}
		messages = agent.InitialMessages(cfg)
	}
	session := newGatewaySession(newID(), time.Now().UTC(), messages)
	if err := s.saveSession(session); err != nil {
		writeError(w, http.StatusInternalServerError, "session save failed: "+err.Error())
		return
	}
	s.attachSessionStore(session)
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
	messages := session.Thread.Messages()
	status := session.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            session.ID,
		"created":       session.Created,
		"message_count": len(messages),
		"messages":      messages,
		"running":       status.Running,
		"status":        status,
	})
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, session.Status())
}

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	emit := func(event protocol.Event) bool {
		if err := enc.Encode(event); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	events, unsubscribe := session.Subscribe()
	defer unsubscribe()
	if !emit(protocol.Event{Type: protocol.EventSessionStatus, Data: session.Status()}) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			if !emit(event) {
				return
			}
		}
	}
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
	streamEvents(w, func(emit func(protocol.Event)) error {
		a, err := s.agentFor(req)
		if err != nil {
			return err
		}
		statusReq := s.statusRunRequest(req)
		err = session.Thread.Run(r.Context(), sessionpkg.RunnerFunc(a.RunMessages), req.Prompt, func(event protocol.Event) {
			if event.Type == protocol.EventRunStarted {
				session.beginRunStatus(statusReq)
			}
			session.observeRunEvent(event)
			emit(event)
		})
		if !errors.Is(err, sessionpkg.ErrBusy) {
			if saveErr := s.saveSession(session); saveErr != nil {
				log.Printf("gateway session save failed id=%s: %v", session.ID, saveErr)
			}
		}
		return err
	})
}

func (s *Server) handleSessionCancel(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cancelled": session.Thread.Cancel(),
	})
}

func (s *Server) session(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *Server) saveSession(session *Session) error {
	if s.store == nil {
		return nil
	}
	return s.store.Save(session)
}

func (s *Server) attachSessionStore(session *Session) {
	if s.store == nil || session == nil {
		return
	}
	session.eventRecorder = func(event protocol.Event) {
		if err := s.store.AppendEvent(session, event); err != nil {
			log.Printf("gateway session event save failed id=%s type=%s: %v", session.ID, event.Type, err)
		}
	}
}

func (s *Server) agentFor(req RunRequest) (*agent.Agent, error) {
	cfg := s.cfg
	if strings.TrimSpace(req.Profile) != "" {
		cfg.Profile = config.NormalizeProfileName(req.Profile)
		if err := cfg.ApplyProfileMetadata(); err != nil {
			return nil, err
		}
	}
	if req.Provider != "" {
		cfg.Provider = req.Provider
	}
	if req.Model != "" {
		cfg.Model = req.Model
	}
	cfg.ApplyModelProviderDefaults()
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
	return agent.New(cfg, prov, s.registry), nil
}

func (s *Server) statusRunRequest(req RunRequest) RunRequest {
	cfg := s.cfg
	if strings.TrimSpace(req.Profile) != "" {
		cfg.Profile = config.NormalizeProfileName(req.Profile)
		_ = cfg.ApplyProfileMetadata()
	}
	if req.Provider != "" {
		cfg.Provider = req.Provider
	}
	if req.Model != "" {
		cfg.Model = req.Model
	}
	cfg.ApplyModelProviderDefaults()
	if req.ReasoningEffort != "" {
		cfg.ReasoningEffort = req.ReasoningEffort
	}
	if req.Thinking != "" {
		cfg.Thinking = req.Thinking
	}
	if req.MaxToolRounds > 0 {
		cfg.MaxToolRounds = req.MaxToolRounds
	}
	return RunRequest{
		Provider:        cfg.Provider,
		Model:           cfg.Model,
		Profile:         cfg.Profile,
		Thinking:        cfg.Thinking,
		ReasoningEffort: cfg.ReasoningEffort,
		MaxToolRounds:   cfg.MaxToolRounds,
	}
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
