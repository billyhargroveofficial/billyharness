package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/attachments"
	"github.com/billyhargroveofficial/billyharness/internal/checkpoint"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/runtimehost"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Server struct {
	providerAuth    config.ProviderAuthSnapshot
	providerBinding config.ProviderBinding
	profile         config.ProfileSelection
	runtime         config.RuntimeLimits
	toolPolicy      config.ToolPolicySettings
	diagnostics     config.DiagnosticsSettings
	mcpSettings     config.MCPSettings
	agentClubConfig config.AgentClubSettings
	agentClubStatus gatewayapi.AgentClubReadinessStatus
	hookSettings    config.HookSettings
	instructions    config.InstructionSettings
	gatewayAddr     string
	agent           *agent.Agent
	registry        *tools.Registry
	auth            credentials.Manager
	mux             *http.ServeMux
	authToken       string
	httpSecurity    httpSecurityOptions
	sessions        map[string]*Session
	store           *sessionStore
	storeHealth     gatewayapi.SessionStoreHealth
	agentClub       *agentclub.Registry
	mu              sync.Mutex
}

type ServerOptions struct {
	AuthToken                                string
	SessionStoreDir                          string
	RequireMutationAuth                      bool
	DevAllowUnauthenticatedLoopbackMutations bool
	AgentClubRegistry                        *agentclub.Registry
	AgentClubStatus                          gatewayapi.AgentClubReadinessStatus
}

type ServerSettings struct {
	ProviderAuth    config.ProviderAuthSnapshot
	ProviderBinding config.ProviderBinding
	Profile         config.ProfileSelection
	Runtime         config.RuntimeLimits
	ToolPolicy      config.ToolPolicySettings
	Diagnostics     config.DiagnosticsSettings
	MCP             config.MCPSettings
	AgentClub       config.AgentClubSettings
	Hooks           config.HookSettings
	Instructions    config.InstructionSettings
	GatewayAddr     string
	Auth            config.AuthSettings
}

type Session struct {
	ID             string                  `json:"id"`
	Created        time.Time               `json:"created"`
	Owner          gatewayapi.SessionOwner `json:"owner,omitempty"`
	Thread         *runThread              `json:"-"`
	manifestOnly   bool
	events         *eventHub
	eventRecorder  func(protocol.Event) (protocol.Event, error)
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
type ReadinessResponse = gatewayapi.ReadinessResponse
type ReadinessCheck = gatewayapi.ReadinessCheck
type ReadinessCatalogStatus = gatewayapi.ReadinessCatalogStatus
type ReadinessMCPStatus = gatewayapi.ReadinessMCPStatus
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
type ManagedProcessResponse = gatewayapi.ManagedProcessResponse

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
	return ServerSettingsFromRuntimeHost(runtimehost.SettingsFromConfig(cfg))
}

func ServerSettingsFromRuntimeHost(settings runtimehost.Settings) ServerSettings {
	return ServerSettings{
		ProviderAuth:    settings.ProviderAuth,
		ProviderBinding: settings.ProviderBinding,
		Profile:         settings.Profile,
		Runtime:         settings.Runtime,
		ToolPolicy:      settings.ToolPolicy,
		Diagnostics:     settings.Diagnostics,
		MCP:             settings.MCP,
		AgentClub:       settings.AgentClub,
		Hooks:           settings.Hooks,
		Instructions:    settings.Instructions,
		GatewayAddr:     settings.GatewayAddr,
		Auth:            settings.Auth,
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
		agentClubConfig: settings.AgentClub,
		agentClubStatus: opts.AgentClubStatus,
		hookSettings:    settings.Hooks,
		instructions:    settings.Instructions,
		gatewayAddr:     settings.GatewayAddr,
		agent:           runtimehost.NewAgent(runtimeHostSettingsFromServerSettings(settings), prov, registry),
		registry:        registry,
		auth:            credentials.NewManagerFromAuthSettings(settings.Auth),
		mux:             http.NewServeMux(),
		sessions:        map[string]*Session{},
		agentClub:       opts.AgentClubRegistry,
	}
	if strings.TrimSpace(opts.SessionStoreDir) != "" {
		s.store = newSessionStore(opts.SessionStoreDir)
		loaded, diagnostics, err := s.store.LoadAllWithDiagnostics()
		s.storeHealth = diagnostics
		if err != nil {
			log.Printf("gateway session store load failed: %v", err)
		}
		for _, item := range diagnostics.Errors {
			log.Printf("gateway session store load skipped entry=%s type=%s session_id=%s session_hash=%s corrupt=%t: %s", item.Entry, item.EntryType, item.SessionID, item.SessionIDHash, item.Corrupt, item.Error)
		}
		for _, session := range loaded {
			s.attachSessionStore(session)
			s.sessions[session.ID] = session
		}
	}
	opts.AuthToken = strings.TrimSpace(opts.AuthToken)
	s.authToken = opts.AuthToken
	s.httpSecurity = httpSecurityOptions{
		requireMutationAuth:                      opts.RequireMutationAuth,
		devAllowUnauthenticatedLoopbackMutations: opts.DevAllowUnauthenticatedLoopbackMutations,
	}
	s.routes()
	return s
}

func agentSettingsFromServerSettings(settings ServerSettings) agent.Settings {
	return runtimeHostSettingsFromServerSettings(settings).AgentSettings()
}

func runtimeHostSettingsFromServerSettings(settings ServerSettings) runtimehost.Settings {
	return runtimehost.Settings{
		ProviderAuth:    settings.ProviderAuth,
		ProviderBinding: settings.ProviderBinding,
		Profile:         settings.Profile,
		Runtime:         settings.Runtime,
		ToolPolicy:      settings.ToolPolicy,
		Diagnostics:     settings.Diagnostics,
		MCP:             settings.MCP,
		AgentClub:       settings.AgentClub,
		Hooks:           settings.Hooks,
		Instructions:    settings.Instructions,
		GatewayAddr:     settings.GatewayAddr,
		Auth:            settings.Auth,
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
		MCPEnabled:                   settings.MCP.Enabled,
		MCPConfigFiles:               settings.MCP.ConfigFiles,
		MCPAllowedServers:            settings.MCP.AllowedServers,
		MCPPromoteServerInstructions: settings.MCP.PromoteServerInstructions,
		MCPServers:                   settings.MCP.Servers,
	}.MCPSettings()
	settings.AgentClub = config.Config{
		AgentClubConfigFiles: settings.AgentClub.ConfigFiles,
	}.AgentClubSettings()
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
	return s.httpSecurityMiddleware(s.mux)
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

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	actor := sessionOwnerFromRequest(r)
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
	if !sessionOwnerBodyMatchesActor(owner, actor) {
		writeError(w, http.StatusForbidden, "session owner scope mismatch")
		return
	}
	if sessionOwnerEmpty(owner) && !sessionOwnerEmpty(actor) {
		owner = actor
	}
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

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	actor := sessionOwnerFromRequest(r)
	sessions := s.allSessions()
	summaries := make([]SessionSummary, 0, len(sessions))
	for _, session := range sessions {
		if err := authorizeSessionAccess(session, actor, sessionAccessRead); err != nil {
			continue
		}
		summaries = append(summaries, sessionSummary(session))
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Created.After(summaries[j].Created)
	})
	writeJSON(w, http.StatusOK, SessionListResponse{Sessions: summaries})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(session, true))
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, session.Status())
}

func (s *Server) handleSessionContextStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.sessionContextResponse(session))
}

func (s *Server) handleSessionInspect(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessRead)
	if !ok {
		return
	}
	inspection, err := s.inspectSession(session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session inspect failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inspection)
}

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessRead)
	if !ok {
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
	var initialReplay []protocol.Event
	if hasAfterSeq {
		if s.store == nil {
			writeError(w, http.StatusConflict, "session history unavailable: no session store")
			return
		}
		initialReplay, err = s.store.ReplayEventsAfter(session.ID, afterSeq)
		if err != nil {
			writeSessionReplayError(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}
	emit := func(event protocol.Event) bool {
		return writeNDJSONEvent(w, flusher, event)
	}
	events, unsubscribe := session.Subscribe()
	defer unsubscribe()
	cursor := afterSeq
	emitReplay := func(replayed []protocol.Event) bool {
		for _, event := range replayed {
			if event.Seq > cursor {
				cursor = event.Seq
			}
			if !emit(event) {
				return false
			}
		}
		return true
	}
	flushReplay := func() bool {
		replayed, err := s.store.ReplayEventsAfter(session.ID, afterSeq)
		if err != nil {
			_ = emit(protocol.Event{Type: protocol.EventRunFailed, Data: "event replay failed: " + err.Error()})
			return false
		}
		return emitReplay(replayed)
	}
	if hasAfterSeq {
		if !emitReplay(initialReplay) {
			return
		}
	} else if !hasAfterSeq {
		if !emit(protocol.Event{Type: protocol.EventSessionStatus, Data: session.Status()}) {
			return
		}
	}
	if !follow {
		return
	}
	if hasAfterSeq && s.store != nil {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				afterSeq = cursor
				if !flushReplay() {
					return
				}
				if event.Seq == 0 && !emit(event) {
					return
				}
			case <-ticker.C:
				afterSeq = cursor
				if !flushReplay() {
					return
				}
			}
		}
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
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

func writeSessionReplayError(w http.ResponseWriter, err error) {
	var corrupt *eventlog.CorruptionError
	if errors.As(err, &corrupt) {
		writeError(w, http.StatusConflict, "corrupt session event history: "+err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "session event replay failed: "+err.Error())
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
	if !runRequestHasInput(req) {
		writeError(w, http.StatusBadRequest, "prompt or attachment required")
		return
	}
	if err := validateAttachmentRefs(req.Attachments); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	streamEvents(r.Context(), w, func(emit func(protocol.Event)) error {
		settings, err := s.runSettingsForRequest(r.Context(), req)
		if err != nil {
			return err
		}
		a, err := s.agentForRunSettings(settings)
		if err != nil {
			return err
		}
		messages := agent.InitialMessagesFromSettings(settings.instructions)
		messages = append(messages, protocol.UserMessage(req.Prompt, req.Attachments))
		_, err = a.RunMessagesWithPromptOptions(r.Context(), messages, promptSubmitOptionsFromRun(req, "gateway"), emit)
		return err
	})
}

func (s *Server) handleSessionRun(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !runRequestHasInput(req) {
		writeError(w, http.StatusBadRequest, "prompt or attachment required")
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
		var validation *sessionInputValidationError
		if errors.As(err, &validation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "input admission failed: "+err.Error())
		return
	}
	if admission.Duplicate && admission.State != sessionInputAdmitted {
		writeError(w, http.StatusConflict, fmt.Sprintf("input_id %q is already %s", admission.InputID, admission.State))
		return
	}
	streamEvents(r.Context(), w, func(emit func(protocol.Event)) error {
		req.InterruptPolicy = interruptPolicy
		return s.runAdmittedSessionInput(r.Context(), session, req, admission, "gateway_session", emit)
	})
}

func (s *Server) runAdmittedSessionInput(ctx context.Context, session *Session, req RunRequest, admission gatewayapi.SessionInputResponse, source string, emit func(protocol.Event)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		emit = func(protocol.Event) {}
	}
	interruptPolicy, err := normalizeInterruptPolicy(req.InterruptPolicy)
	if err != nil {
		return err
	}
	completePreflightFailure := func(err error) error {
		if err == nil {
			return nil
		}
		if completeErr := s.completeSessionInputWithReason(session, admission.InputID, 0, "preflight_failed", err.Error()); completeErr != nil {
			return errors.Join(err, fmt.Errorf("complete preflight-failed input: %w", completeErr))
		}
		return err
	}
	if err := s.applySessionInterruptPolicy(ctx, session, interruptPolicy); err != nil {
		return completePreflightFailure(err)
	}
	settings, err := s.runSettingsForRequest(ctx, req)
	if err != nil {
		return completePreflightFailure(err)
	}
	a, err := s.agentForSessionRunSettings(session, settings)
	if err != nil {
		return completePreflightFailure(err)
	}
	statusReq := runRequestFromSettings(settings)
	userMessage := protocol.UserMessage(req.Prompt, req.Attachments)
	contextEpochAdmission := s.sessionContextEpochAdmission(ctx, session, settings, userMessage)
	runSeq, err := s.promoteSessionInput(session, admission.InputID)
	if err != nil {
		return completePreflightFailure(err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	var persistenceMu sync.Mutex
	var persistenceErr error
	setPersistenceErr := func(err error) {
		if err == nil {
			return
		}
		persistenceMu.Lock()
		if persistenceErr == nil {
			persistenceErr = err
			cancelRun()
		}
		persistenceMu.Unlock()
	}
	getPersistenceErr := func() error {
		persistenceMu.Lock()
		defer persistenceMu.Unlock()
		return persistenceErr
	}
	if source == "" {
		source = "gateway_session"
	}
	err = session.Thread.RunMessage(runCtx, RunnerFunc(func(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
		return a.RunMessagesWithPromptOptions(ctx, messages, promptSubmitOptionsFromRun(req, source), emit)
	}), userMessage, func(event protocol.Event) {
		if getPersistenceErr() != nil {
			return
		}
		if event.Type == protocol.EventRunStarted {
			drift, err := session.beginRunStatusWithContextEpoch(statusReq, runSeq, contextEpochAdmission.Run, contextEpochAdmission.Current)
			if err != nil {
				setPersistenceErr(err)
				return
			}
			event = addContextEpochToRunStarted(event, contextEpochAdmission.Run, drift)
		}
		observed, ok, err := session.observeRunEvent(event)
		if err != nil {
			setPersistenceErr(err)
			return
		}
		if ok {
			emit(observed)
		}
	})
	hadPersistenceErr := false
	if persistErr := getPersistenceErr(); persistErr != nil {
		err = persistErr
		hadPersistenceErr = true
	}
	if !hadPersistenceErr && !errors.Is(err, ErrBusy) {
		if saveErr := s.saveSession(session); saveErr != nil {
			persistErr := fmt.Errorf("session save failed after run: %w", saveErr)
			session.markPersistenceFailure(persistErr)
			emit(protocol.Event{Type: protocol.EventSessionStatus, Data: session.Status()})
			if err == nil {
				err = persistErr
			} else {
				err = errors.Join(err, persistErr)
			}
		}
	}
	terminalStatus := "completed"
	if err != nil {
		terminalStatus = "failed"
		if errors.Is(err, ErrBusy) {
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
}

func (s *Server) handleSessionInput(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	var req gatewayapi.SessionInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !sessionInputRequestHasInput(req) {
		writeError(w, http.StatusBadRequest, "prompt or attachment required")
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
		var validation *sessionInputValidationError
		if errors.As(err, &validation) {
			writeError(w, http.StatusBadRequest, err.Error())
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

func (s *Server) handleSessionInputComplete(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	var req gatewayapi.SessionInputCompleteRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	inputID := strings.TrimSpace(r.PathValue("input_id"))
	resp, err := s.completeSessionInputResponse(session, inputID, 0, req.TerminalStatus, req.FailureReason)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "input completion failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUserInputAnswer(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
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
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
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
			return fmt.Errorf("session save failed after interrupt: %w", saveErr)
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

func (s *Server) promoteSessionInput(session *Session, inputID string) (int64, error) {
	if s == nil || s.store == nil {
		if session == nil {
			return 0, nil
		}
		return session.Status().RunSeq + 1, nil
	}
	return s.store.PromoteInputForNextRun(session, inputID)
}

func (s *Server) completeSessionInput(session *Session, inputID string, runSeq int64, terminalStatus string) error {
	_, err := s.completeSessionInputResponse(session, inputID, runSeq, terminalStatus, "")
	return err
}

func (s *Server) completeSessionInputWithReason(session *Session, inputID string, runSeq int64, terminalStatus, failureReason string) error {
	_, err := s.completeSessionInputResponse(session, inputID, runSeq, terminalStatus, failureReason)
	return err
}

func (s *Server) completeSessionInputResponse(session *Session, inputID string, runSeq int64, terminalStatus, failureReason string) (gatewayapi.SessionInputResponse, error) {
	if s == nil || s.store == nil {
		return gatewayapi.SessionInputResponse{
			InputID:        strings.TrimSpace(inputID),
			State:          sessionInputCompleted,
			TerminalStatus: defaultSessionInputTerminalStatus(terminalStatus),
			FailureReason:  strings.TrimSpace(failureReason),
		}, nil
	}
	return s.store.CompleteInputWithOptions(session, inputID, sessionInputCompleteOptions{
		RunSeq:         runSeq,
		TerminalStatus: terminalStatus,
		FailureReason:  failureReason,
	})
}

func sessionInputRequestFromRun(req RunRequest) gatewayapi.SessionInputRequest {
	return gatewayapi.SessionInputRequest{
		InputID:         req.InputID,
		Prompt:          req.Prompt,
		Attachments:     append([]protocol.AttachmentRef(nil), req.Attachments...),
		InterruptPolicy: req.InterruptPolicy,
		ClientID:        req.ClientID,
		ClientType:      req.ClientType,
		Metadata:        req.Metadata,
	}
}

func runRequestHasInput(req RunRequest) bool {
	return strings.TrimSpace(req.Prompt) != "" || len(req.Attachments) > 0
}

func sessionInputRequestHasInput(req gatewayapi.SessionInputRequest) bool {
	return strings.TrimSpace(req.Prompt) != "" || len(req.Attachments) > 0
}

func validateAttachmentRefs(refs []protocol.AttachmentRef) error {
	if len(refs) == 0 {
		return nil
	}
	store := attachments.DefaultStore()
	for _, ref := range refs {
		if ref.Kind != "" && ref.Kind != protocol.AttachmentKindImage {
			return fmt.Errorf("unsupported attachment kind %q", ref.Kind)
		}
		if _, err := store.Resolve(ref); err != nil {
			return fmt.Errorf("invalid attachment %s: %w", firstNonEmpty(ref.ID, ref.FileName, "unknown"), err)
		}
	}
	return nil
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
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	waitCtx, cancel := context.WithTimeout(r.Context(), gatewayInterruptWaitTimeout)
	defer cancel()
	cancelled, err := session.interruptActiveRunAndWait(waitCtx, "cancelled by session cancel endpoint")
	if cancelled {
		if saveErr := s.saveSession(session); saveErr != nil {
			writeError(w, http.StatusInternalServerError, "session save failed after cancel: "+saveErr.Error())
			return
		}
	}
	if err != nil {
		writeError(w, http.StatusGatewayTimeout, "cancel active session run: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, CancelSessionResponse{Cancelled: cancelled})
}

func (s *Server) handleSessionUndo(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
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
	var stored storedTurnChange
	var found bool
	var err error
	if req.Preview {
		stored, found, err = s.store.FindTurnChange(session.ID, req.ChangeID)
	} else {
		stored, found, err = s.store.FindUndoableTurnChange(session.ID, req.ChangeID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "turn change replay failed: "+err.Error())
		return
	}
	if !found {
		if req.Preview {
			writeError(w, http.StatusNotFound, "turn change not found")
		} else {
			writeError(w, http.StatusNotFound, "undoable turn change not found")
		}
		return
	}
	record, err := checkpoint.LoadVerified(stored.Data.PatchOutputRef, stored.Data.PatchOutputRefSHA256)
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
	restoreOpts := checkpoint.RestoreOptions{
		WorkspaceRoots:       s.toolPolicy.WorkspaceRoots,
		PatchOutputRef:       stored.Data.PatchOutputRef,
		PatchOutputRefSHA256: stored.Data.PatchOutputRefSHA256,
	}
	result, err := checkpoint.RestoreWithOptions(record, restoreOpts)
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
	if _, err := session.publish(protocol.Event{
		Type:      protocol.EventTurnChangeReverted,
		RunID:     change.RunID,
		TurnID:    change.TurnID,
		StepID:    change.StepID,
		CallID:    change.CallID,
		AttemptID: change.AttemptID,
		Data:      change,
	}); err != nil {
		if _, rollbackErr := checkpoint.RedoWithOptions(record, restoreOpts); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "session event persistence failed after checkpoint restore; rollback failed: "+rollbackErr.Error()+"; original persistence error: "+err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "session event persistence failed after checkpoint restore; restore rolled back: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gatewayapi.SessionUndoResponse{
		ChangeID:      stored.Data.ChangeID,
		RestoredFiles: result.RestoredFiles,
		Change:        change,
	})
}

func (s *Server) handleSessionRedo(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	if session.Status().Running || (session.Thread != nil && session.Thread.Running()) {
		writeError(w, http.StatusConflict, "redo denied during active run; cancel or wait for the run to finish first")
		return
	}
	if s.store == nil {
		writeError(w, http.StatusNotFound, "session turn changes are unavailable without a session store")
		return
	}
	stored, ok, err := s.store.FindRedoTurnChange(session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "turn change replay failed: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "redo change not found")
		return
	}
	record, err := checkpoint.LoadVerified(stored.Data.PatchOutputRef, stored.Data.PatchOutputRefSHA256)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "checkpoint load failed: "+err.Error())
		return
	}
	restoreOpts := checkpoint.RestoreOptions{
		WorkspaceRoots:       s.toolPolicy.WorkspaceRoots,
		PatchOutputRef:       stored.Data.PatchOutputRef,
		PatchOutputRefSHA256: stored.Data.PatchOutputRefSHA256,
	}
	result, err := checkpoint.RedoWithOptions(record, restoreOpts)
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
		writeError(w, http.StatusInternalServerError, "checkpoint redo failed: "+err.Error())
		return
	}
	change := stored.Data
	change.Status = "redone"
	change.Summary = "redone " + change.ChangeID
	if _, err := session.publish(protocol.Event{
		Type:      protocol.EventTurnChangeRecorded,
		RunID:     change.RunID,
		TurnID:    change.TurnID,
		StepID:    change.StepID,
		CallID:    change.CallID,
		AttemptID: change.AttemptID,
		Data:      change,
	}); err != nil {
		if _, rollbackErr := checkpoint.RestoreWithOptions(record, restoreOpts); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "session event persistence failed after checkpoint redo; rollback failed: "+rollbackErr.Error()+"; original persistence error: "+err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "session event persistence failed after checkpoint redo; redo rolled back: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gatewayapi.SessionUndoResponse{
		ChangeID:      stored.Data.ChangeID,
		RestoredFiles: result.RestoredFiles,
		Change:        change,
	})
}

func (s *Server) session(id string) (*Session, bool) {
	session, ok, _ := s.sessionWithError(id)
	return session, ok
}

func (s *Server) sessionWithError(id string) (*Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok || session == nil || !session.manifestOnly || s.store == nil {
		return session, ok, nil
	}
	loaded, err := s.store.loadSessionID(id)
	if err != nil {
		log.Printf("gateway session lazy load failed id=%s: %v", id, err)
		return nil, true, err
	}
	s.attachSessionStore(loaded)
	s.sessions[loaded.ID] = loaded
	session = loaded
	return session, ok, nil
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
		ID:               session.ID,
		Created:          session.Created,
		MessageCount:     status.MessageCount,
		AttachmentCount:  status.AttachmentCount,
		ImageSubmissions: status.ImageSubmissions,
		Messages:         messages,
		Running:          status.Running,
		Owner:            status.Owner,
		Status:           status,
	}
}

func sessionSummary(session *Session) SessionSummary {
	status := session.Status()
	return SessionSummary{
		ID:               session.ID,
		Created:          session.Created,
		Running:          status.Running,
		RunSeq:           status.RunSeq,
		MessageCount:     status.MessageCount,
		AttachmentCount:  status.AttachmentCount,
		ImageSubmissions: status.ImageSubmissions,
		DroppedEvents:    status.DroppedEvents,
		LastEvent:        status.LastEvent,
		LastEventAt:      status.LastEventAt,
		Model:            status.Model,
		Provider:         status.Provider,
		Profile:          status.Profile,
		ReasoningEffort:  status.ReasoningEffort,
		AccessMode:       status.AccessMode,
		Owner:            status.Owner,
		LastError:        status.LastError,
	}
}

func normalizeSessionOwner(owner gatewayapi.SessionOwner) gatewayapi.SessionOwner {
	owner.ClientID = strings.TrimSpace(owner.ClientID)
	owner.ClientType = strings.ToLower(strings.TrimSpace(owner.ClientType))
	owner.TUIChatID = strings.TrimSpace(owner.TUIChatID)
	owner.Profile = strings.TrimSpace(owner.Profile)
	owner.Model = strings.TrimSpace(owner.Model)
	return owner
}

func (s *Server) runSettingsForRequest(ctx context.Context, req RunRequest) (runSettings, error) {
	mayOverrideProviderModel := s.requestMayOverrideProviderModel(ctx)
	overrides := s.runOverrideSettingsForRequest(ctx, req)
	if err := s.validateRunProviderModelOverride(overrides); err != nil {
		return runSettings{}, err
	}
	settings, err := config.RuntimeDiffSettingsWithRunOverrides(s.runtimeDiffSettings(), overrides)
	if err != nil {
		return runSettings{}, err
	}
	if !mayOverrideProviderModel {
		settings.Provider = s.providerBinding
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
		AgentClub:   s.agentClubConfig,
		Hooks:       s.hookSettings,
		GatewayAddr: s.gatewayAddr,
	}
}

func runSettingsFromRuntimeDiffSettings(settings config.RuntimeDiffSettings) runSettings {
	instructions := runtimehost.InstructionSettingsFromRuntimeDiffSettings(settings)
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
	hostSettings := runtimeHostSettingsFromRunSettings(settings)
	prov, err := runtimehost.NewProvider(hostSettings)
	if err != nil {
		return nil, err
	}
	return runtimehost.NewAgent(hostSettings, prov, s.registry), nil
}

func (s *Server) agentForSessionRunSettings(session *Session, settings runSettings) (*agent.Agent, error) {
	hostSettings := runtimeHostSettingsFromRunSettings(settings)
	prov, err := runtimehost.NewProvider(hostSettings)
	if err != nil {
		return nil, err
	}
	return runtimehost.NewAgent(hostSettings, prov, s.registry, runtimehost.WithAskUser(func(ctx context.Context, request protocol.UserInputRequestEvent, emit func(protocol.Event)) (protocol.UserInputAnswerEvent, error) {
		return session.askUser(ctx, request, emit)
	})), nil
}

func runtimeHostSettingsFromRunSettings(settings runSettings) runtimehost.Settings {
	return runtimehost.Settings{
		ProviderBinding: settings.provider,
		ProviderCaps:    config.Config{Provider: settings.provider.Provider.Provider, Model: settings.provider.Model.Model}.ProviderCapabilitySnapshot(),
		Profile:         settings.profile,
		Runtime:         settings.runtime,
		ToolPolicy:      settings.toolPolicy,
		Diagnostics:     settings.diagnostics,
		MCP:             settings.mcp,
		Hooks:           settings.hooks,
		Instructions:    settings.instructions,
		Auth:            settings.provider.Auth,
	}
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

func (s *Server) runOverrideSettingsForRequest(ctx context.Context, req RunRequest) config.RunOverrideSettings {
	overrides := config.RunOverrideSettings{
		Provider:        req.Provider,
		Model:           req.Model,
		Profile:         req.Profile,
		Thinking:        req.Thinking,
		ReasoningEffort: req.ReasoningEffort,
		MaxToolRounds:   req.MaxToolRounds,
		AccessMode:      req.AccessMode,
	}
	if !s.requestMayOverrideProviderModel(ctx) {
		overrides.Provider = ""
		overrides.Model = ""
		overrides.Thinking = ""
		overrides.ReasoningEffort = ""
	}
	overrides.MaxToolRounds = clampMaxToolRoundsOverride(s.runtime.MaxToolRounds, overrides.MaxToolRounds)
	overrides.AccessMode = clampAccessModeOverride(s.toolPolicy.AccessMode, overrides.AccessMode)
	return overrides
}

func (s *Server) validateRunProviderModelOverride(overrides config.RunOverrideSettings) error {
	providerID := modelinfo.NormalizeProvider(overrides.Provider)
	if providerID == "" {
		return nil
	}
	modelID := strings.TrimSpace(overrides.Model)
	if modelID == "" && s != nil {
		modelID = s.providerBinding.Model.Model
	}
	modelID = modelinfo.NormalizeAlias(modelID)
	if modelID == "" {
		return nil
	}
	info := modelinfo.Lookup(modelID)
	if info.Provider == "" || info.Provider == providerID || modelinfo.Provider(providerID).Custom {
		return nil
	}
	return fmt.Errorf("provider/model conflict: model %q belongs to provider %q, not %q", modelID, info.Provider, providerID)
}
