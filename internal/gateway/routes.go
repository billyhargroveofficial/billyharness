package gateway

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type routeSpec struct {
	Method   string
	Pattern  string
	Handler  http.HandlerFunc
	Summary  string
	Request  string
	Response string
}

type RouteDoc struct {
	Method    string
	Pattern   string
	Summary   string
	Request   string
	Response  string
	AuthClass string
}

func RouteDocs() []RouteDoc {
	return (&Server{}).RouteDocs()
}

func (s *Server) routes() {
	for _, route := range s.routeSpecs() {
		s.mux.HandleFunc(route.Method+" "+route.Pattern, route.Handler)
	}
}

func (s *Server) RouteDocs() []RouteDoc {
	specs := s.routeSpecs()
	out := make([]RouteDoc, 0, len(specs))
	for _, spec := range specs {
		out = append(out, RouteDoc{
			Method:    spec.Method,
			Pattern:   spec.Pattern,
			Summary:   spec.Summary,
			Request:   spec.Request,
			Response:  spec.Response,
			AuthClass: AuthClassFor(spec.Method, spec.Pattern),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pattern == out[j].Pattern {
			return out[i].Method < out[j].Method
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}

func (s *Server) routeSpecs() []routeSpec {
	return []routeSpec{
		{Method: "GET", Pattern: "/health", Handler: s.handleHealth, Summary: "Return minimal process health", Response: "HealthResponse"},
		{Method: "GET", Pattern: "/ready", Handler: s.handleReadiness, Summary: "Return readiness checks and catalog status", Response: "ReadinessResponse"},
		{Method: "GET", Pattern: "/v1/auth/status", Handler: s.handleAuthStatus, Summary: "Return redacted provider credential status", Response: "credentials.Status"},
		{Method: "POST", Pattern: "/v1/auth/deepseek", Handler: s.handleDeepSeekAuth, Summary: "Save a DeepSeek API key", Request: "DeepSeekAuthRequest", Response: "credentials.ProviderStatus"},
		{Method: "POST", Pattern: "/v1/auth/codex/import", Handler: s.handleCodexImport, Summary: "Import Codex OAuth credentials", Request: "CodexImportRequest", Response: "credentials.ProviderStatus"},
		{Method: "GET", Pattern: "/v1/config", Handler: s.handleConfigStatus, Summary: "Return effective config with provenance", Response: "ConfigStatusResponse"},
		{Method: "GET", Pattern: "/v1/tools", Handler: s.handleTools, Summary: "Return native tool catalog", Response: "[]protocol.ToolSpec"},
		{Method: "GET", Pattern: "/v1/mcp", Handler: s.handleMCP, Summary: "Return MCP status snapshot", Response: "mcpstatus.Response"},
		{Method: "GET", Pattern: "/v1/processes", Handler: s.handleManagedProcesses, Summary: "Return managed shell processes", Response: "ManagedProcessResponse"},
		{Method: "POST", Pattern: "/v1/run", Handler: s.handleRun, Summary: "Run a one-shot prompt stream", Request: "RunRequest", Response: "NDJSON protocol.Event"},
		{Method: "GET", Pattern: "/v1/sessions", Handler: s.handleListSessions, Summary: "List gateway sessions", Response: "SessionListResponse"},
		{Method: "POST", Pattern: "/v1/sessions", Handler: s.handleCreateSession, Summary: "Create a gateway session", Request: "CreateSessionRequest", Response: "SessionResponse"},
		{Method: "GET", Pattern: "/v1/sessions/{id}", Handler: s.handleGetSession, Summary: "Fetch a session snapshot", Response: "SessionResponse"},
		{Method: "GET", Pattern: "/v1/sessions/{id}/status", Handler: s.handleSessionStatus, Summary: "Fetch a compact session status", Response: "SessionStatus"},
		{Method: "GET", Pattern: "/v1/sessions/{id}/context", Handler: s.handleSessionContextStatus, Summary: "Inspect session context accounting", Response: "SessionContextResponse"},
		{Method: "GET", Pattern: "/v1/sessions/{id}/inspect", Handler: s.handleSessionInspect, Summary: "Inspect stored session diagnostics", Response: "SessionInspectResponse"},
		{Method: "GET", Pattern: "/v1/sessions/{id}/events", Handler: s.handleSessionEvents, Summary: "Replay or follow session events", Response: "NDJSON protocol.Event"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/agentclub/events", Handler: s.handleAgentClubEventIngress, Summary: "Admit an agent-club event as queued session input", Request: "agentclub.EventRequest", Response: "agentclub.EventAdmissionResponse"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/inputs", Handler: s.handleSessionInput, Summary: "Admit a queued session input", Request: "SessionInputRequest", Response: "SessionInputResponse"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/inputs/{input_id}/complete", Handler: s.handleSessionInputComplete, Summary: "Mark a queued input terminal", Request: "SessionInputCompleteRequest", Response: "SessionInputResponse"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/run", Handler: s.handleSessionRun, Summary: "Run a prompt inside an existing session", Request: "RunRequest", Response: "NDJSON protocol.Event"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/user_input/{request_id}/answer", Handler: s.handleUserInputAnswer, Summary: "Answer a pending user-input request", Request: "UserInputAnswerRequest", Response: "UserInputResponse"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/user_input/{request_id}/reject", Handler: s.handleUserInputReject, Summary: "Reject a pending user-input request", Request: "UserInputRejectRequest", Response: "UserInputResponse"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/undo", Handler: s.handleSessionUndo, Summary: "Undo the latest recorded filesystem change", Request: "SessionUndoRequest", Response: "SessionUndoResponse"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/redo", Handler: s.handleSessionRedo, Summary: "Redo the latest reverted filesystem change", Response: "SessionUndoResponse"},
		{Method: "POST", Pattern: "/v1/sessions/{id}/cancel", Handler: s.handleSessionCancel, Summary: "Cancel an active session run", Response: "CancelSessionResponse"},
	}
}

func AuthClassFor(method, pattern string) string {
	path := fillRoutePattern(pattern)
	if path == "/health" {
		return "public"
	}
	req := &http.Request{Method: strings.ToUpper(strings.TrimSpace(method)), URL: &url.URL{Path: path}}
	if !isGatewayV1Route(req) {
		return "public"
	}
	if isGatewayMutation(req) {
		return "bearer-mutation"
	}
	return "local-read"
}

func fillRoutePattern(pattern string) string {
	var b strings.Builder
	inWildcard := false
	for _, r := range pattern {
		switch {
		case r == '{':
			inWildcard = true
			b.WriteByte('x')
		case r == '}':
			inWildcard = false
		case !inWildcard:
			b.WriteRune(r)
		}
	}
	return b.String()
}
