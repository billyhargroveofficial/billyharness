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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
	"github.com/billyhargroveofficial/billyharness/internal/trace"
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
	eventRecorder func(protocol.Event) protocol.Event
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

type HealthResponse struct {
	OK       bool   `json:"ok"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type ConfigStatusResponse struct {
	Config   map[string]any         `json:"config"`
	Values   []config.ResolvedValue `json:"values"`
	Warnings []string               `json:"warnings,omitempty"`
}

type SessionListResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

type SessionSummary struct {
	ID              string    `json:"id"`
	Created         time.Time `json:"created"`
	Running         bool      `json:"running"`
	RunSeq          int64     `json:"run_seq"`
	MessageCount    int       `json:"message_count"`
	LastEvent       string    `json:"last_event,omitempty"`
	LastEventAt     time.Time `json:"last_event_at,omitempty"`
	Model           string    `json:"model,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	Profile         string    `json:"profile,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

type SessionResponse struct {
	ID           string             `json:"id"`
	Created      time.Time          `json:"created"`
	MessageCount int                `json:"message_count"`
	Messages     []protocol.Message `json:"messages,omitempty"`
	Running      bool               `json:"running"`
	Status       SessionStatus      `json:"status"`
}

type SessionContextResponse struct {
	ID                      string               `json:"id"`
	MessageCount            int                  `json:"message_count"`
	EstimatedTokens         int64                `json:"estimated_tokens"`
	ContextWindowTokens     int64                `json:"context_window_tokens"`
	ContextCompactTokens    int64                `json:"context_compact_tokens"`
	PercentUsed             float64              `json:"percent_used"`
	CompactThresholdPercent float64              `json:"compact_threshold_percent"`
	OverCompactThreshold    bool                 `json:"over_compact_threshold"`
	Estimator               string               `json:"estimator"`
	TopContributors         []ContextContributor `json:"top_contributors,omitempty"`
}

type ContextContributor struct {
	Index           int    `json:"index"`
	Role            string `json:"role"`
	Chars           int    `json:"chars"`
	EstimatedTokens int64  `json:"estimated_tokens"`
	Preview         string `json:"preview,omitempty"`
}

type CancelSessionResponse struct {
	Cancelled bool `json:"cancelled"`
}

type BenchmarkListResponse struct {
	Dir  string                `json:"dir"`
	Runs []BenchmarkRunSummary `json:"runs"`
}

type BenchmarkRunSummary struct {
	RunID           string    `json:"run_id"`
	CreatedAt       time.Time `json:"created_at"`
	Harness         string    `json:"harness,omitempty"`
	ProfileHash     string    `json:"profile_hash,omitempty"`
	TasksPath       string    `json:"tasks_path,omitempty"`
	TaskCount       int       `json:"task_count,omitempty"`
	ManifestJSON    string    `json:"manifest_json"`
	ResultsJSONL    string    `json:"results_jsonl,omitempty"`
	EventsJSONL     string    `json:"events_jsonl,omitempty"`
	PayloadsDir     string    `json:"payloads_dir,omitempty"`
	ResultsPresent  bool      `json:"results_present"`
	EventsPresent   bool      `json:"events_present"`
	PayloadsPresent bool      `json:"payloads_present"`
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
		if aborted := s.abortActiveSessions("gateway shutdown"); aborted > 0 {
			log.Printf("gateway shutdown aborted %d active session(s)", aborted)
		}
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
	s.mux.HandleFunc("GET /v1/benchmarks", s.handleBenchmarks)
	s.mux.HandleFunc("GET /v1/tools", s.handleTools)
	s.mux.HandleFunc("GET /v1/mcp", s.handleMCP)
	s.mux.HandleFunc("POST /v1/run", s.handleRun)
	s.mux.HandleFunc("GET /v1/sessions", s.handleListSessions)
	s.mux.HandleFunc("POST /v1/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /v1/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("GET /v1/sessions/{id}/status", s.handleSessionStatus)
	s.mux.HandleFunc("GET /v1/sessions/{id}/context", s.handleSessionContextStatus)
	s.mux.HandleFunc("GET /v1/sessions/{id}/events", s.handleSessionEvents)
	s.mux.HandleFunc("POST /v1/sessions/{id}/run", s.handleSessionRun)
	s.mux.HandleFunc("POST /v1/sessions/{id}/cancel", s.handleSessionCancel)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		OK:       true,
		Provider: s.cfg.Provider,
		Model:    s.cfg.Model,
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
	writeJSON(w, http.StatusOK, ConfigStatusResponse{
		Config:   resolved.SanitizedConfig(),
		Values:   resolved.SanitizedValues(),
		Warnings: resolved.Warnings,
	})
}

func (s *Server) handleBenchmarks(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if dir == "" {
		dir = defaultBenchmarkRunsDir()
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(config.BillyHomeDir(), dir)
	}
	runs, err := listBenchmarkRuns(dir, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, BenchmarkListResponse{
		Dir:  filepath.Clean(dir),
		Runs: runs,
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
	writeJSON(w, http.StatusCreated, sessionResponse(session, false))
}

func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	sessions := s.allSessions()
	summaries := make([]SessionSummary, 0, len(sessions))
	for _, session := range sessions {
		summaries = append(summaries, sessionSummary(session))
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Created.After(summaries[j].Created)
	})
	writeJSON(w, http.StatusOK, SessionListResponse{Sessions: summaries})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(session, true))
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, session.Status())
}

func (s *Server) handleSessionContextStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, sessionContextResponse(s.cfg, session))
}

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	afterSeq, hasAfterSeq, err := parseEventReplayCursor(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	follow, err := parseEventFollow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	emit := func(event protocol.Event) bool {
		return writeNDJSONEvent(w, flusher, event)
	}
	events, unsubscribe := session.Subscribe()
	defer unsubscribe()
	cursor := afterSeq
	if hasAfterSeq && s.store != nil {
		replayed, err := s.store.ReplayEventsAfter(session.ID, afterSeq)
		if err != nil {
			_ = emit(protocol.Event{Type: protocol.EventRunFailed, Data: "event replay failed: " + err.Error()})
			return
		}
		for _, event := range replayed {
			if event.Seq > cursor {
				cursor = event.Seq
			}
			if !emit(event) {
				return
			}
		}
	} else if !hasAfterSeq {
		if !emit(protocol.Event{Type: protocol.EventSessionStatus, Data: session.Status()}) {
			return
		}
	}
	if !follow {
		return
	}
	if hasAfterSeq {
		for {
			select {
			case event := <-events:
				if event.Seq == 0 || event.Seq > cursor {
					if event.Seq > cursor {
						cursor = event.Seq
					}
					if !emit(event) {
						return
					}
				}
			default:
				goto live
			}
		}
	}
live:
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			if hasAfterSeq {
				if event.Seq != 0 && event.Seq <= cursor {
					continue
				}
				if event.Seq > cursor {
					cursor = event.Seq
				}
			}
			if !emit(event) {
				return
			}
		}
	}
}

func parseEventReplayCursor(r *http.Request) (int64, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("after_seq"))
	if raw == "" {
		return 0, false, nil
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 {
		return 0, true, fmt.Errorf("after_seq must be a non-negative integer")
	}
	return seq, true, nil
}

func parseEventFollow(r *http.Request) (bool, error) {
	raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("follow")))
	if raw == "" {
		return true, nil
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("follow must be true or false")
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
	writeJSON(w, http.StatusOK, CancelSessionResponse{Cancelled: session.Thread.Cancel()})
}

func (s *Server) session(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *Server) allSessions() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		out = append(out, session)
	}
	return out
}

func (s *Server) abortActiveSessions(reason string) int {
	count := 0
	for _, session := range s.allSessions() {
		if session.abortActiveRun(reason) {
			count++
			if err := s.saveSession(session); err != nil {
				log.Printf("gateway session save failed id=%s after abort: %v", session.ID, err)
			}
		}
	}
	return count
}

func sessionResponse(session *Session, includeMessages bool) SessionResponse {
	status := session.Status()
	var messages []protocol.Message
	if includeMessages {
		messages = session.messages()
	}
	return SessionResponse{
		ID:           session.ID,
		Created:      session.Created,
		MessageCount: status.MessageCount,
		Messages:     messages,
		Running:      status.Running,
		Status:       status,
	}
}

func sessionSummary(session *Session) SessionSummary {
	status := session.Status()
	return SessionSummary{
		ID:              session.ID,
		Created:         session.Created,
		Running:         status.Running,
		RunSeq:          status.RunSeq,
		MessageCount:    status.MessageCount,
		LastEvent:       status.LastEvent,
		LastEventAt:     status.LastEventAt,
		Model:           status.Model,
		Provider:        status.Provider,
		Profile:         status.Profile,
		ReasoningEffort: status.ReasoningEffort,
		LastError:       status.LastError,
	}
}

func sessionContextResponse(cfg config.Config, session *Session) SessionContextResponse {
	messages := session.messages()
	estimatedTokens := estimateGatewayMessagesTokens(messages)
	contextWindow := cfg.ContextWindowTokens
	compactAt := int64(cfg.ContextCompactTokens)
	var percentUsed float64
	if contextWindow > 0 {
		percentUsed = float64(estimatedTokens) / float64(contextWindow) * 100
	}
	var thresholdPercent float64
	if contextWindow > 0 && compactAt > 0 {
		thresholdPercent = float64(compactAt) / float64(contextWindow) * 100
	}
	contributors := make([]ContextContributor, 0, len(messages))
	for i, msg := range messages {
		chars := gatewayMessageChars(msg)
		if chars == 0 {
			continue
		}
		contributors = append(contributors, ContextContributor{
			Index:           i,
			Role:            string(msg.Role),
			Chars:           chars,
			EstimatedTokens: int64((chars + 3) / 4),
			Preview:         previewGatewayMessage(msg.Content, 120),
		})
	}
	sort.Slice(contributors, func(i, j int) bool {
		if contributors[i].EstimatedTokens == contributors[j].EstimatedTokens {
			return contributors[i].Index < contributors[j].Index
		}
		return contributors[i].EstimatedTokens > contributors[j].EstimatedTokens
	})
	if len(contributors) > 5 {
		contributors = contributors[:5]
	}
	return SessionContextResponse{
		ID:                      session.ID,
		MessageCount:            len(messages),
		EstimatedTokens:         estimatedTokens,
		ContextWindowTokens:     contextWindow,
		ContextCompactTokens:    compactAt,
		PercentUsed:             percentUsed,
		CompactThresholdPercent: thresholdPercent,
		OverCompactThreshold:    compactAt > 0 && estimatedTokens >= compactAt,
		Estimator:               "chars_div_4",
		TopContributors:         contributors,
	}
}

func estimateGatewayMessagesTokens(messages []protocol.Message) int64 {
	var chars int
	for _, msg := range messages {
		chars += gatewayMessageChars(msg)
	}
	if chars == 0 {
		return 0
	}
	return int64((chars + 3) / 4)
}

func gatewayMessageChars(msg protocol.Message) int {
	chars := len(msg.Content) + len(msg.Name) + len(msg.ToolCallID) + len(string(msg.Role))
	for _, call := range msg.ToolCalls {
		chars += len(call.ID) + len(call.Name) + len(call.Arguments)
	}
	return chars
}

func previewGatewayMessage(content string, maxChars int) string {
	content = strings.Join(strings.Fields(content), " ")
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	if maxChars <= 3 {
		return content[:maxChars]
	}
	return content[:maxChars-3] + "..."
}

func defaultBenchmarkRunsDir() string {
	return filepath.Join(config.BillyHomeDir(), "bench-runs")
}

func listBenchmarkRuns(root string, limit int) ([]BenchmarkRunSummary, error) {
	root = filepath.Clean(root)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []BenchmarkRunSummary
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err == nil && strings.Count(rel, string(os.PathSeparator)) >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "-manifest.json") {
			return nil
		}
		run, err := readBenchmarkRunSummary(path)
		if err == nil && run.RunID != "" {
			runs = append(runs, run)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].RunID > runs[j].RunID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func readBenchmarkRunSummary(manifestPath string) (BenchmarkRunSummary, error) {
	bytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return BenchmarkRunSummary{}, err
	}
	var manifest trace.Manifest
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		return BenchmarkRunSummary{}, err
	}
	manifestDir := filepath.Dir(manifestPath)
	resultsPath := resolveBenchmarkArtifactPath(manifestDir, manifest.ResultsJSONL)
	eventsPath := resolveBenchmarkArtifactPath(manifestDir, manifest.EventsJSONL)
	payloadsDir := resolveBenchmarkArtifactPath(manifestDir, manifest.PayloadsDir)
	return BenchmarkRunSummary{
		RunID:           manifest.RunID,
		CreatedAt:       manifest.CreatedAt,
		Harness:         manifest.Harness,
		ProfileHash:     manifest.ProfileHash,
		TasksPath:       manifest.TasksPath,
		TaskCount:       manifest.TaskCount,
		ManifestJSON:    filepath.Clean(manifestPath),
		ResultsJSONL:    resultsPath,
		EventsJSONL:     eventsPath,
		PayloadsDir:     payloadsDir,
		ResultsPresent:  fileExists(resultsPath),
		EventsPresent:   fileExists(eventsPath),
		PayloadsPresent: dirExists(payloadsDir),
	}, nil
}

func resolveBenchmarkArtifactPath(manifestDir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	candidates := []string{
		filepath.Clean(path),
		filepath.Join(manifestDir, filepath.Base(path)),
		filepath.Join(manifestDir, path),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) || dirExists(candidate) {
			return absBenchmarkPath(candidate)
		}
	}
	return filepath.Join(manifestDir, filepath.Base(path))
}

func absBenchmarkPath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
	session.eventRecorder = func(event protocol.Event) protocol.Event {
		stored, err := s.store.AppendEvent(session, event)
		if err != nil {
			log.Printf("gateway session event save failed id=%s type=%s: %v", session.ID, event.Type, err)
			return event
		}
		return stored
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
	flusher, _ := w.(http.Flusher)
	emit := func(event protocol.Event) {
		_ = writeNDJSONEvent(w, flusher, event)
	}
	if err := run(emit); err != nil {
		emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
	}
}

func writeNDJSONEvent(w http.ResponseWriter, flusher http.Flusher, event protocol.Event) bool {
	body, err := json.Marshal(event)
	if err != nil {
		return false
	}
	body = append([]byte(secrets.Redact(string(body))), '\n')
	if _, err := w.Write(body); err != nil {
		return false
	}
	if flusher != nil {
		flusher.Flush()
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, `{"error":"failed to encode JSON"}`, http.StatusInternalServerError)
		return
	}
	body = append([]byte(secrets.Redact(string(body))), '\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
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
