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
	"sync/atomic"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/checkpoint"
	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
	"github.com/billyhargroveofficial/billyharness/internal/trace"
)

type Server struct {
	providerAuth    config.ProviderAuthSnapshot
	providerBinding config.ProviderBinding
	profile         config.ProfileSelection
	runtime         config.RuntimeLimits
	toolPolicy      config.ToolPolicySettings
	diagnostics     config.DiagnosticsSettings
	mcpSettings     config.MCPSettings
	hookSettings    config.HookSettings
	instructions    config.InstructionSettings
	gatewayAddr     string
	agent           *agent.Agent
	registry        *tools.Registry
	auth            credentials.Manager
	mux             *http.ServeMux
	authToken       string
	sessions        map[string]*Session
	store           *sessionStore
	mu              sync.Mutex
}

type ServerOptions struct {
	AuthToken       string
	SessionStoreDir string
}

type ServerSettings struct {
	ProviderAuth    config.ProviderAuthSnapshot
	ProviderBinding config.ProviderBinding
	Profile         config.ProfileSelection
	Runtime         config.RuntimeLimits
	ToolPolicy      config.ToolPolicySettings
	Diagnostics     config.DiagnosticsSettings
	MCP             config.MCPSettings
	Hooks           config.HookSettings
	Instructions    config.InstructionSettings
	GatewayAddr     string
	Auth            config.AuthSettings
}

type Session struct {
	ID             string                  `json:"id"`
	Created        time.Time               `json:"created"`
	Owner          gatewayapi.SessionOwner `json:"owner,omitempty"`
	Thread         *sessionpkg.Session     `json:"-"`
	events         *eventHub
	eventRecorder  func(protocol.Event) protocol.Event
	storeSnapshots sessionStoreSnapshots
	activeRunID    string
	terminalRunIDs map[string]struct{}
	pendingInput   *pendingUserInput
	mu             sync.Mutex
	status         SessionStatus
}

type RunRequest = gatewayapi.RunRequest
type CreateSessionRequest = gatewayapi.CreateSessionRequest
type DeepSeekAuthRequest = gatewayapi.DeepSeekAuthRequest
type CodexImportRequest = gatewayapi.CodexImportRequest
type HealthResponse = gatewayapi.HealthResponse
type ConfigStatusResponse = gatewayapi.ConfigStatusResponse
type SessionListResponse = gatewayapi.SessionListResponse
type SessionSummary = gatewayapi.SessionSummary
type SessionResponse = gatewayapi.SessionResponse
type SessionOwner = gatewayapi.SessionOwner
type SessionContextResponse = gatewayapi.SessionContextResponse
type ContextContributor = gatewayapi.ContextContributor
type ContextSource = gatewayapi.ContextSource
type ContextThreshold = gatewayapi.ContextThreshold
type CancelSessionResponse = gatewayapi.CancelSessionResponse
type UserInputAnswerRequest = gatewayapi.UserInputAnswerRequest
type UserInputRejectRequest = gatewayapi.UserInputRejectRequest
type UserInputResponse = gatewayapi.UserInputResponse
type BenchmarkListResponse = gatewayapi.BenchmarkListResponse
type BenchmarkRunSummary = gatewayapi.BenchmarkRunSummary

type runSettings struct {
	provider     config.ProviderBinding
	profile      config.ProfileSelection
	runtime      config.RuntimeLimits
	toolPolicy   config.ToolPolicySettings
	diagnostics  config.DiagnosticsSettings
	mcp          config.MCPSettings
	hooks        config.HookSettings
	instructions config.InstructionSettings
}

func NewServer(cfg config.Config, prov provider.Provider, registry *tools.Registry) *Server {
	return NewServerFromSettings(ServerSettingsFromConfig(cfg), prov, registry)
}

func NewServerWithOptions(cfg config.Config, prov provider.Provider, registry *tools.Registry, opts ServerOptions) *Server {
	return NewServerWithOptionsFromSettings(ServerSettingsFromConfig(cfg), prov, registry, opts)
}

func ServerSettingsFromConfig(cfg config.Config) ServerSettings {
	return ServerSettings{
		ProviderAuth:    cfg.ProviderAuthSnapshot(),
		ProviderBinding: cfg.ProviderBinding(),
		Profile:         cfg.ProfileSelection(),
		Runtime:         cfg.RuntimeLimits(),
		ToolPolicy:      cfg.ToolPolicySettings(),
		Diagnostics:     cfg.DiagnosticsSettings(),
		MCP:             cfg.MCPSettings(),
		Hooks:           cfg.HookSettings(),
		Instructions:    cfg.InstructionSettings(),
		GatewayAddr:     cfg.GatewayAddr,
		Auth:            cfg.AuthSettings(),
	}
}

func NewServerFromSettings(settings ServerSettings, prov provider.Provider, registry *tools.Registry) *Server {
	return NewServerWithOptionsFromSettings(settings, prov, registry, ServerOptions{})
}

func NewServerWithOptionsFromSettings(settings ServerSettings, prov provider.Provider, registry *tools.Registry, opts ServerOptions) *Server {
	settings = cloneServerSettings(settings)
	s := &Server{
		providerAuth:    settings.ProviderAuth,
		providerBinding: settings.ProviderBinding,
		profile:         settings.Profile,
		runtime:         settings.Runtime,
		toolPolicy:      settings.ToolPolicy,
		diagnostics:     settings.Diagnostics,
		mcpSettings:     settings.MCP,
		hookSettings:    settings.Hooks,
		instructions:    settings.Instructions,
		gatewayAddr:     settings.GatewayAddr,
		agent:           agent.NewFromSettings(agentSettingsFromServerSettings(settings), prov, registry),
		registry:        registry,
		auth:            credentials.NewManagerFromAuthSettings(settings.Auth),
		mux:             http.NewServeMux(),
		sessions:        map[string]*Session{},
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

func agentSettingsFromServerSettings(settings ServerSettings) agent.Settings {
	return agent.Settings{
		ProviderBinding: settings.ProviderBinding,
		Profile:         settings.Profile,
		Runtime:         settings.Runtime,
		ToolPolicy:      settings.ToolPolicy,
		MCP:             settings.MCP,
		Hooks:           settings.Hooks,
		Instructions:    settings.Instructions,
	}
}

func cloneServerSettings(settings ServerSettings) ServerSettings {
	settings.ToolPolicy.WorkspaceRoots = append([]string(nil), settings.ToolPolicy.WorkspaceRoots...)
	settings.ToolPolicy.ProjectDocFallbacks = append([]string(nil), settings.ToolPolicy.ProjectDocFallbacks...)
	settings.Diagnostics = config.Config{
		DiagnosticsEnabled:     settings.Diagnostics.Enabled,
		DiagnosticsConfigFiles: settings.Diagnostics.ConfigFiles,
		DiagnosticsCommands:    settings.Diagnostics.Commands,
	}.DiagnosticsSettings()
	settings.MCP = config.Config{
		MCPEnabled:        settings.MCP.Enabled,
		MCPConfigFiles:    settings.MCP.ConfigFiles,
		MCPAllowedServers: settings.MCP.AllowedServers,
		MCPServers:        settings.MCP.Servers,
	}.MCPSettings()
	settings.Hooks = config.Config{
		HooksEnabled:    settings.Hooks.Enabled,
		HookConfigFiles: settings.Hooks.ConfigFiles,
		Hooks:           settings.Hooks.Hooks,
	}.HookSettings()
	settings.Instructions.WorkspaceRoots = append([]string(nil), settings.Instructions.WorkspaceRoots...)
	settings.Instructions.ProjectDocFallbacks = append([]string(nil), settings.Instructions.ProjectDocFallbacks...)
	return settings
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
	s.mux.HandleFunc("POST /v1/sessions/{id}/inputs", s.handleSessionInput)
	s.mux.HandleFunc("POST /v1/sessions/{id}/run", s.handleSessionRun)
	s.mux.HandleFunc("POST /v1/sessions/{id}/user_input/{request_id}/answer", s.handleUserInputAnswer)
	s.mux.HandleFunc("POST /v1/sessions/{id}/user_input/{request_id}/reject", s.handleUserInputReject)
	s.mux.HandleFunc("POST /v1/sessions/{id}/undo", s.handleSessionUndo)
	s.mux.HandleFunc("POST /v1/sessions/{id}/cancel", s.handleSessionCancel)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		OK:       true,
		Provider: s.providerAuth.Provider,
		Model:    s.providerAuth.Model,
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
	resolved, err := config.Resolve(config.RuntimeDiffOverridesFromSettings(base.Config, s.runtimeDiffSettings(), config.SourceGateway)...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ConfigStatusResponse{
		Config:      resolved.SanitizedConfig(),
		Values:      resolved.SanitizedValues(),
		Diagnostics: resolved.Config.DiagnosticSnapshot(),
		Warnings:    resolved.Warnings,
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
	mcpSettings := s.registry.MCPSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"config_files": mcpSettings.ConfigFiles,
		"allowed":      mcpSettings.AllowedServers,
		"enabled":      mcpSettings.Enabled,
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
	profile := s.profile.Profile
	if strings.TrimSpace(req.Profile) != "" {
		profile = config.NormalizeProfileName(req.Profile)
	}
	if len(messages) == 0 {
		instructions := s.instructions
		instructions.Profile = config.ProfileSelection{Profile: profile}
		messages = agent.InitialMessagesFromSettings(instructions)
	}
	owner := normalizeSessionOwner(req.Owner)
	if owner.Profile == "" {
		owner.Profile = profile
	}
	if owner.Model == "" {
		owner.Model = s.providerBinding.Model.Model
	}
	session := newGatewaySessionWithOwner(newID(), time.Now().UTC(), messages, owner)
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
	writeJSON(w, http.StatusOK, sessionContextResponse(s.runtime, session))
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
		settings, err := s.runSettingsForRequest(req)
		if err != nil {
			return err
		}
		a, err := s.agentForRunSettings(settings)
		if err != nil {
			return err
		}
		return a.RunWithPromptOptions(r.Context(), req.Prompt, promptSubmitOptionsFromRun(req, "gateway"), emit)
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
	interruptPolicy, err := normalizeInterruptPolicy(req.InterruptPolicy)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	admission, err := s.admitSessionInput(session, sessionInputRequestFromRun(req))
	if err != nil {
		var conflict *sessionInputConflictError
		if errors.As(err, &conflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "input admission failed: "+err.Error())
		return
	}
	if admission.Duplicate && admission.State != sessionInputAdmitted {
		writeError(w, http.StatusConflict, fmt.Sprintf("input_id %q is already %s", admission.InputID, admission.State))
		return
	}
	runSeq := session.Status().RunSeq + 1
	streamEvents(w, func(emit func(protocol.Event)) error {
		if err := s.applySessionInterruptPolicy(r.Context(), session, interruptPolicy); err != nil {
			return err
		}
		settings, err := s.runSettingsForRequest(req)
		if err != nil {
			return err
		}
		a, err := s.agentForSessionRunSettings(session, settings)
		if err != nil {
			return err
		}
		statusReq := runRequestFromSettings(settings)
		if err := s.promoteSessionInput(session, admission.InputID, runSeq); err != nil {
			return err
		}
		err = session.Thread.Run(r.Context(), sessionpkg.RunnerFunc(func(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
			return a.RunMessagesWithPromptOptions(ctx, messages, promptSubmitOptionsFromRun(req, "gateway_session"), emit)
		}), req.Prompt, func(event protocol.Event) {
			if event.Type == protocol.EventRunStarted {
				session.beginRunStatus(statusReq)
			}
			if observed, ok := session.observeRunEvent(event); ok {
				emit(observed)
			}
		})
		if !errors.Is(err, sessionpkg.ErrBusy) {
			if saveErr := s.saveSession(session); saveErr != nil {
				log.Printf("gateway session save failed id=%s: %v", session.ID, saveErr)
			}
		}
		terminalStatus := "completed"
		if err != nil {
			terminalStatus = "failed"
			if errors.Is(err, sessionpkg.ErrBusy) {
				terminalStatus = "busy"
			}
		}
		if completeErr := s.completeSessionInput(session, admission.InputID, runSeq, terminalStatus); completeErr != nil {
			log.Printf("gateway session input complete failed id=%s input=%s: %v", session.ID, admission.InputID, completeErr)
			if err == nil {
				return completeErr
			}
		}
		return err
	})
}

func (s *Server) handleSessionInput(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	var req gatewayapi.SessionInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "prompt required")
		return
	}
	if _, err := normalizeInterruptPolicy(req.InterruptPolicy); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.admitSessionInput(session, req)
	if err != nil {
		var conflict *sessionInputConflictError
		if errors.As(err, &conflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "input admission failed: "+err.Error())
		return
	}
	status := http.StatusCreated
	if resp.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleUserInputAnswer(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	var req UserInputAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	requestID := strings.TrimSpace(r.PathValue("request_id"))
	answer, err := session.answerUserInput(requestID, req)
	if err != nil {
		writeUserInputError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, UserInputResponse{RequestID: answer.RequestID, Status: "answered"})
}

func (s *Server) handleUserInputReject(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	var req UserInputRejectRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	requestID := strings.TrimSpace(r.PathValue("request_id"))
	reject, err := session.rejectUserInput(requestID, req)
	if err != nil {
		writeUserInputError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, UserInputResponse{RequestID: reject.RequestID, Status: "rejected"})
}

func writeUserInputError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoPendingUserInput), errors.Is(err, errUserInputRequestUnknown):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

const gatewayInterruptWaitTimeout = 3 * time.Second

func normalizeInterruptPolicy(policy string) (string, error) {
	policy = strings.ToLower(strings.TrimSpace(policy))
	switch policy {
	case "", gatewayapi.InterruptPolicyInterrupt:
		return policy, nil
	default:
		return "", fmt.Errorf("unsupported interrupt_policy %q", policy)
	}
}

func (s *Server) applySessionInterruptPolicy(ctx context.Context, session *Session, policy string) error {
	if policy != gatewayapi.InterruptPolicyInterrupt {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, gatewayInterruptWaitTimeout)
	defer cancel()
	interrupted, err := session.interruptActiveRunAndWait(waitCtx, "interrupted by newer session run")
	if interrupted {
		if saveErr := s.saveSession(session); saveErr != nil {
			log.Printf("gateway session save failed id=%s after interrupt: %v", session.ID, saveErr)
		}
	}
	if err != nil {
		return fmt.Errorf("interrupt active session run: %w", err)
	}
	return nil
}

func (s *Server) admitSessionInput(session *Session, req gatewayapi.SessionInputRequest) (gatewayapi.SessionInputResponse, error) {
	if s == nil || s.store == nil {
		if strings.TrimSpace(req.InputID) == "" {
			req.InputID = newID()
		}
		inputID, err := cleanSessionInputID(req.InputID)
		if err != nil {
			return gatewayapi.SessionInputResponse{}, err
		}
		return gatewayapi.SessionInputResponse{InputID: inputID, State: sessionInputAdmitted}, nil
	}
	return s.store.AdmitInput(session, req)
}

func (s *Server) promoteSessionInput(session *Session, inputID string, runSeq int64) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.PromoteInput(session, inputID, runSeq)
}

func (s *Server) completeSessionInput(session *Session, inputID string, runSeq int64, terminalStatus string) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.CompleteInput(session, inputID, runSeq, terminalStatus)
}

func sessionInputRequestFromRun(req RunRequest) gatewayapi.SessionInputRequest {
	return gatewayapi.SessionInputRequest{
		InputID:         req.InputID,
		Prompt:          req.Prompt,
		InterruptPolicy: req.InterruptPolicy,
		ClientID:        req.ClientID,
		Metadata:        req.Metadata,
	}
}

func promptSubmitOptionsFromRun(req RunRequest, fallbackSource string) agent.PromptSubmitOptions {
	source := fallbackSource
	if strings.HasPrefix(req.ClientID, "telegram") {
		source = "telegram"
	} else if strings.HasPrefix(req.ClientID, "tui") {
		source = "tui"
	} else if strings.TrimSpace(req.ClientID) != "" {
		source = req.ClientID
	}
	return agent.PromptSubmitOptions{
		Source:   source,
		Metadata: req.Metadata,
	}
}

func (s *Server) handleSessionCancel(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, CancelSessionResponse{Cancelled: session.Thread.Cancel()})
}

func (s *Server) handleSessionUndo(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	var req gatewayapi.SessionUndoRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	if session.Status().Running || (session.Thread != nil && session.Thread.Running()) {
		writeError(w, http.StatusConflict, "undo denied during active run; cancel or wait for the run to finish first")
		return
	}
	if s.store == nil {
		writeError(w, http.StatusNotFound, "session turn changes are unavailable without a session store")
		return
	}
	stored, ok, err := s.store.FindTurnChange(session.ID, req.ChangeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "turn change replay failed: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "turn change not found")
		return
	}
	record, err := checkpoint.Load(stored.Data.PatchOutputRef)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "checkpoint load failed: "+err.Error())
		return
	}
	if req.Preview {
		patch, truncated := checkpoint.Preview(record, 64*1024)
		resp := gatewayapi.SessionUndoResponse{
			ChangeID:       stored.Data.ChangeID,
			Preview:        true,
			Patch:          patch,
			PatchTruncated: truncated,
			Change:         stored.Data,
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	result, err := checkpoint.Restore(record)
	if err != nil {
		resp := gatewayapi.SessionUndoResponse{
			ChangeID:  stored.Data.ChangeID,
			Conflicts: result.Conflicts,
			Change:    stored.Data,
		}
		if errors.Is(err, checkpoint.ErrConflict) {
			writeJSON(w, http.StatusConflict, resp)
			return
		}
		writeError(w, http.StatusInternalServerError, "checkpoint restore failed: "+err.Error())
		return
	}
	change := stored.Data
	change.Status = "reverted"
	change.Summary = "reverted " + change.ChangeID
	session.publish(protocol.Event{
		Type:      protocol.EventTurnChangeReverted,
		RunID:     change.RunID,
		TurnID:    change.TurnID,
		StepID:    change.StepID,
		CallID:    change.CallID,
		AttemptID: change.AttemptID,
		Data:      change,
	})
	writeJSON(w, http.StatusOK, gatewayapi.SessionUndoResponse{
		ChangeID:      stored.Data.ChangeID,
		RestoredFiles: result.RestoredFiles,
		Change:        change,
	})
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
		Owner:        status.Owner,
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
		DroppedEvents:   status.DroppedEvents,
		LastEvent:       status.LastEvent,
		LastEventAt:     status.LastEventAt,
		Model:           status.Model,
		Provider:        status.Provider,
		Profile:         status.Profile,
		ReasoningEffort: status.ReasoningEffort,
		AccessMode:      status.AccessMode,
		Owner:           status.Owner,
		LastError:       status.LastError,
	}
}

func normalizeSessionOwner(owner gatewayapi.SessionOwner) gatewayapi.SessionOwner {
	owner.ClientType = strings.ToLower(strings.TrimSpace(owner.ClientType))
	owner.TUIChatID = strings.TrimSpace(owner.TUIChatID)
	owner.Profile = strings.TrimSpace(owner.Profile)
	owner.Model = strings.TrimSpace(owner.Model)
	return owner
}

func sessionContextResponse(limits config.RuntimeLimits, session *Session) SessionContextResponse {
	return clientux.BuildContextResponse(limits, session.ID, session.messages())
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
	s.refreshSessionSnapshots(session)
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

func (s *Server) refreshSessionSnapshots(session *Session) {
	if s == nil || session == nil {
		return
	}
	snapshot := s.sessionSnapshot(session)
	var specs []protocol.ToolSpec
	if s.registry != nil {
		specs = s.registry.SnapshotWithToolPolicy(context.Background(), snapshot.ToolPolicy).Specs()
	}
	runtimeSnapshot := runstate.NewSnapshot(runstate.SnapshotInput{
		Provider:   snapshot.Provider,
		Profile:    snapshot.Profile,
		Runtime:    snapshot.Runtime,
		ToolPolicy: snapshot.ToolPolicy,
		MCP:        snapshot.MCP,
	}, session.messages(), specs)
	session.setStoreSnapshots(sessionStoreSnapshots{
		Config:        sessionConfigSnapshot(snapshot.ProviderAuth, snapshot.Runtime, snapshot.ToolPolicy, snapshot.MCP, snapshot.GatewayAddr),
		ModelProvider: runtimeSnapshot.Metadata(),
		MCP:           s.mcpSnapshot(snapshot.MCP),
	})
}

type sessionSnapshotProjection struct {
	ProviderAuth config.ProviderAuthSnapshot
	Provider     config.ProviderBinding
	Profile      config.ProfileSelection
	Runtime      config.RuntimeLimits
	ToolPolicy   config.ToolPolicySettings
	Diagnostics  config.DiagnosticsSettings
	MCP          config.MCPSettings
	GatewayAddr  string
}

func (s *Server) sessionSnapshot(session *Session) sessionSnapshotProjection {
	snapshot := sessionSnapshotProjection{
		ProviderAuth: s.providerAuth,
		Provider:     s.providerBinding,
		Profile:      s.profile,
		Runtime:      s.runtime,
		ToolPolicy:   s.toolPolicy,
		Diagnostics:  s.diagnostics,
		MCP:          s.mcpSettings,
		GatewayAddr:  s.gatewayAddr,
	}
	if session == nil {
		return normalizeSessionSnapshot(snapshot)
	}
	status := session.Status()
	if strings.TrimSpace(status.Provider) != "" {
		snapshot.Provider.Provider.Provider = status.Provider
	}
	if strings.TrimSpace(status.Model) != "" {
		snapshot.Provider.Model.Model = status.Model
	}
	if strings.TrimSpace(status.Profile) != "" {
		snapshot.Profile = config.ProfileSelection{Profile: config.NormalizeProfileName(status.Profile)}
	}
	if strings.TrimSpace(status.ReasoningEffort) != "" {
		snapshot.Provider.Model.ReasoningEffort = status.ReasoningEffort
	}
	if strings.TrimSpace(status.AccessMode) != "" {
		snapshot.ToolPolicy.AccessMode = config.NormalizeAccessMode(status.AccessMode)
	}
	return normalizeSessionSnapshot(snapshot)
}

func normalizeSessionSnapshot(snapshot sessionSnapshotProjection) sessionSnapshotProjection {
	model := modelinfo.NormalizeAlias(snapshot.Provider.Model.Model)
	if snapshot.Provider.Model.DisableSpark && modelinfo.IsSparkModel(model) {
		model = "gpt-5.4-mini"
	}
	providerID := modelinfo.ProviderForModel(model, snapshot.Provider.Provider.Provider)
	snapshot.Provider.Model.Model = model
	snapshot.Provider.Provider.Provider = providerID
	snapshot.ProviderAuth.Provider = providerID
	snapshot.ProviderAuth.Model = model
	snapshot.ProviderAuth.Profile = snapshot.Profile.Profile
	snapshot.ProviderAuth.ReasoningEffort = snapshot.Provider.Model.ReasoningEffort
	snapshot.ToolPolicy.AccessMode = config.NormalizeAccessMode(snapshot.ToolPolicy.AccessMode)
	return snapshot
}

func sessionConfigSnapshot(providerAuth config.ProviderAuthSnapshot, limits config.RuntimeLimits, toolPolicy config.ToolPolicySettings, mcpSettings config.MCPSettings, gatewayAddr string) map[string]any {
	return map[string]any{
		"provider":                         providerAuth.Provider,
		"model":                            providerAuth.Model,
		"profile":                          providerAuth.Profile,
		"thinking":                         providerAuth.Thinking,
		"reasoning_effort":                 providerAuth.ReasoningEffort,
		"disable_spark":                    providerAuth.DisableSpark,
		"max_tokens":                       limits.MaxTokens,
		"max_tool_rounds":                  limits.MaxToolRounds,
		"max_parallel_tools":               limits.MaxParallelTools,
		"provider_max_retries":             limits.ProviderMaxRetries,
		"context_window_tokens":            limits.ContextWindowTokens,
		"context_compact_tokens":           limits.ContextCompactTokens,
		"context_compact_keep":             limits.ContextCompactKeep,
		"context_compact_max_chars":        limits.ContextCompactMaxChars,
		"context_compact_strategy":         limits.ContextCompactStrategy,
		"context_compact_summary_provider": limits.ContextCompactSummaryProvider,
		"context_compact_summary_model":    limits.ContextCompactSummaryModel,
		"web_summary_mode":                 toolPolicy.WebSummaryMode,
		"web_summary_provider":             toolPolicy.WebSummaryProvider,
		"web_summary_model":                toolPolicy.WebSummaryModel,
		"web_summary_max_input_tokens":     toolPolicy.WebSummaryMaxInputTokens,
		"web_summary_max_output_tokens":    toolPolicy.WebSummaryMaxOutputTokens,
		"web_cache_enabled":                toolPolicy.WebCacheEnabled,
		"web_cache_ttl_ms":                 toolPolicy.WebCacheTTL.Milliseconds(),
		"web_cache_max_bytes":              toolPolicy.WebCacheMaxBytes,
		"workspace_roots":                  append([]string(nil), toolPolicy.WorkspaceRoots...),
		"max_tool_output_bytes":            toolPolicy.MaxToolOutputBytes,
		"auto_approve_dangerous":           toolPolicy.AutoApproveDangerous,
		"access_mode":                      toolPolicy.AccessMode,
		"store_reasoning_content":          toolPolicy.StoreReasoningContent,
		"gateway_addr":                     gatewayAddr,
		"mcp_enabled":                      mcpSettings.Enabled,
		"mcp_config_files":                 append([]string(nil), mcpSettings.ConfigFiles...),
		"mcp_allowed_servers":              append([]string(nil), mcpSettings.AllowedServers...),
	}
}

func (s *Server) mcpSnapshot(mcpSettings config.MCPSettings) map[string]any {
	if s != nil && s.registry != nil {
		registrySettings := s.registry.MCPSettings()
		if len(registrySettings.Servers) > 0 || len(registrySettings.ConfigFiles) > 0 {
			mcpSettings = registrySettings
		}
	}
	var runtimeStatuses []any
	connected := 0
	if s.registry != nil {
		for _, status := range s.registry.MCPStatuses() {
			runtimeStatuses = append(runtimeStatuses, status)
			if status.Connected {
				connected++
			}
		}
	}
	return map[string]any{
		"enabled":        mcpSettings.Enabled,
		"config_files":   append([]string(nil), mcpSettings.ConfigFiles...),
		"allowed":        append([]string(nil), mcpSettings.AllowedServers...),
		"server_count":   len(mcpSettings.Servers),
		"status_count":   len(runtimeStatuses),
		"connected":      connected,
		"configured":     mcpServerSummaries(mcpSettings.Servers),
		"runtime_status": runtimeStatuses,
	}
}

func mcpServerSummaries(servers []config.MCPServer) []map[string]any {
	out := make([]map[string]any, 0, len(servers))
	for _, server := range servers {
		transport := "stdio"
		if strings.TrimSpace(server.URL) != "" {
			transport = "http"
		}
		out = append(out, map[string]any{
			"name":           server.Name,
			"enabled":        server.Enabled,
			"required":       server.Required,
			"transport":      transport,
			"command":        filepath.Base(server.Command),
			"url_set":        strings.TrimSpace(server.URL) != "",
			"enabled_tools":  append([]string(nil), server.EnabledTools...),
			"disabled_tools": append([]string(nil), server.DisabledTools...),
		})
	}
	return out
}

func (s *Server) runSettingsForRequest(req RunRequest) (runSettings, error) {
	settings, err := config.RuntimeDiffSettingsWithRunOverrides(s.runtimeDiffSettings(), runOverrideSettingsFromRequest(req))
	if err != nil {
		return runSettings{}, err
	}
	return runSettingsFromRuntimeDiffSettings(settings), nil
}

func (s *Server) runtimeDiffSettings() config.RuntimeDiffSettings {
	return config.RuntimeDiffSettings{
		Provider:    s.providerBinding,
		Profile:     s.profile,
		Runtime:     s.runtime,
		ToolPolicy:  s.toolPolicy,
		Diagnostics: s.diagnostics,
		MCP:         s.mcpSettings,
		Hooks:       s.hookSettings,
		GatewayAddr: s.gatewayAddr,
	}
}

func runSettingsFromRuntimeDiffSettings(settings config.RuntimeDiffSettings) runSettings {
	instructions := config.InstructionSettings{
		Profile:             settings.Profile,
		WorkspaceRoots:      append([]string(nil), settings.ToolPolicy.WorkspaceRoots...),
		ProjectDocMaxBytes:  settings.ToolPolicy.ProjectDocMaxBytes,
		ProjectDocFallbacks: append([]string(nil), settings.ToolPolicy.ProjectDocFallbacks...),
	}
	return runSettings{
		provider:     settings.Provider,
		profile:      settings.Profile,
		runtime:      settings.Runtime,
		toolPolicy:   settings.ToolPolicy,
		diagnostics:  settings.Diagnostics,
		mcp:          settings.MCP,
		hooks:        settings.Hooks,
		instructions: instructions,
	}
}

func (s *Server) agentForRunSettings(settings runSettings) (*agent.Agent, error) {
	prov, err := provider.NewFromBinding(settings.provider)
	if err != nil {
		return nil, err
	}
	return agent.NewFromSettings(agent.Settings{
		ProviderBinding: settings.provider,
		Profile:         settings.profile,
		Runtime:         settings.runtime,
		ToolPolicy:      settings.toolPolicy,
		MCP:             settings.mcp,
		Hooks:           settings.hooks,
		Instructions:    settings.instructions,
	}, prov, s.registry), nil
}

func (s *Server) agentForSessionRunSettings(session *Session, settings runSettings) (*agent.Agent, error) {
	prov, err := provider.NewFromBinding(settings.provider)
	if err != nil {
		return nil, err
	}
	return agent.NewFromSettings(agent.Settings{
		ProviderBinding: settings.provider,
		Profile:         settings.profile,
		Runtime:         settings.runtime,
		ToolPolicy:      settings.toolPolicy,
		MCP:             settings.mcp,
		Hooks:           settings.hooks,
		Instructions:    settings.instructions,
		AskUser: func(ctx context.Context, request protocol.UserInputRequestEvent, emit func(protocol.Event)) (protocol.UserInputAnswerEvent, error) {
			return session.askUser(ctx, request, emit)
		},
	}, prov, s.registry), nil
}

func runRequestFromSettings(settings runSettings) RunRequest {
	return RunRequest{
		Provider:        settings.provider.Provider.Provider,
		Model:           settings.provider.Model.Model,
		Profile:         settings.profile.Profile,
		Thinking:        settings.provider.Model.Thinking,
		ReasoningEffort: settings.provider.Model.ReasoningEffort,
		MaxToolRounds:   settings.runtime.MaxToolRounds,
		AccessMode:      settings.toolPolicy.AccessMode,
	}
}

func runOverrideSettingsFromRequest(req RunRequest) config.RunOverrideSettings {
	return config.RunOverrideSettings{
		Provider:        req.Provider,
		Model:           req.Model,
		Profile:         req.Profile,
		Thinking:        req.Thinking,
		ReasoningEffort: req.ReasoningEffort,
		MaxToolRounds:   req.MaxToolRounds,
		AccessMode:      req.AccessMode,
	}
}

const liveRunStreamBuffer = 256

func streamEvents(w http.ResponseWriter, run func(func(protocol.Event)) error) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	events := make(chan protocol.Event, liveRunStreamBuffer)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for event := range events {
			if !writeNDJSONEvent(w, flusher, event) {
				return
			}
		}
	}()

	var terminalEmitted atomic.Bool
	var droppedEvents atomic.Int64
	var lastQueuedSeq atomic.Int64
	queue := func(event protocol.Event) bool {
		select {
		case <-writerDone:
			return false
		default:
		}
		select {
		case events <- event:
			if event.Seq > 0 {
				lastQueuedSeq.Store(event.Seq)
			}
			return true
		case <-writerDone:
			return false
		default:
			return false
		}
	}
	emitGap := func(block bool) {
		dropped := droppedEvents.Swap(0)
		if dropped <= 0 {
			return
		}
		event := gatewayStreamGapEvent(dropped, lastQueuedSeq.Load())
		if block {
			select {
			case events <- event:
			case <-writerDone:
			}
			return
		}
		if !queue(event) {
			droppedEvents.Add(dropped)
		}
	}
	emit := func(event protocol.Event) {
		if isTerminalRunEvent(event.Type) {
			terminalEmitted.Store(true)
		}
		if droppedEvents.Load() > 0 {
			emitGap(false)
		}
		if !queue(event) {
			droppedEvents.Add(1)
		}
	}
	if err := run(emit); err != nil && !terminalEmitted.Load() {
		emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
	}
	if droppedEvents.Load() > 0 {
		emitGap(true)
	}
	close(events)
	<-writerDone
}

func gatewayStreamGapEvent(dropped, replayAfterSeq int64) protocol.Event {
	return protocol.Event{
		Type:   protocol.EventGatewayStreamGap,
		Source: protocol.EventSourceGateway,
		TS:     time.Now().UTC().Format(time.RFC3339Nano),
		Data: protocol.GatewayStreamGapEvent{
			DroppedEvents:  dropped,
			ReplayAfterSeq: replayAfterSeq,
			Message:        "live stream dropped events; replay /v1/sessions/{id}/events after the last durable seq",
		},
	}
}

func isTerminalRunEvent(eventType protocol.EventType) bool {
	return eventType == protocol.EventRunCompleted || eventType == protocol.EventRunFailed
}

func writeNDJSONEvent(w http.ResponseWriter, flusher http.Flusher, event protocol.Event) bool {
	body, err := marshalRedactedJSON(event)
	if err != nil {
		return false
	}
	body = append(body, '\n')
	if _, err := w.Write(body); err != nil {
		return false
	}
	if flusher != nil {
		flusher.Flush()
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := marshalRedactedJSON(value)
	if err != nil {
		http.Error(w, `{"error":"failed to encode JSON"}`, http.StatusInternalServerError)
		return
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func marshalRedactedJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	redactJSONStrings(decoded)
	return json.Marshal(decoded)
}

func redactJSONStrings(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if text, ok := item.(string); ok {
				v[key] = secrets.Redact(text)
				continue
			}
			redactJSONStrings(item)
		}
	case []any:
		for i, item := range v {
			if text, ok := item.(string); ok {
				v[i] = secrets.Redact(text)
				continue
			}
			redactJSONStrings(item)
		}
	}
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
