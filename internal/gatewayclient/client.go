package gatewayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/displayfmt"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

var (
	ErrSessionNotFound     = errors.New("gateway session not found")
	ErrSessionCorrupt      = errors.New("gateway session corrupt")
	ErrSessionReplayFailed = errors.New("gateway session replay failed")
	ErrNoSessionStore      = errors.New("gateway session history unavailable")
)

type Client struct {
	BaseURL string
	Client  *http.Client
}

type sessionOwnerContextKey struct{}

type RunResult struct {
	EventCount    int
	LastSeq       int64
	Completed     bool
	Failed        bool
	Failure       string
	TerminalEvent protocol.Event
	SeqGap        *EventSeqGapError
	StreamGaps    int
	DroppedEvents int64
}

type StatusError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

type RunFailedError struct {
	Message string
	Event   protocol.Event
}

type EventSeqGapError struct {
	AfterSeq int64
	GotSeq   int64
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: gatewayapi.NormalizeBaseURL(baseURL),
		Client:  &http.Client{Timeout: 0},
	}
}

func WithSessionOwner(ctx context.Context, owner gatewayapi.SessionOwner) context.Context {
	owner = normalizeSessionOwner(owner)
	if owner == (gatewayapi.SessionOwner{}) {
		return ctx
	}
	return context.WithValue(ctx, sessionOwnerContextKey{}, owner)
}

func SessionOwnerFromContext(ctx context.Context) (gatewayapi.SessionOwner, bool) {
	owner, ok := ctx.Value(sessionOwnerContextKey{}).(gatewayapi.SessionOwner)
	if !ok {
		return gatewayapi.SessionOwner{}, false
	}
	owner = normalizeSessionOwner(owner)
	return owner, owner != (gatewayapi.SessionOwner{})
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("gateway %s %s HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("gateway %s %s HTTP %d: %s", e.Method, e.Path, e.StatusCode, body)
}

func (e *StatusError) Is(target error) bool {
	if e == nil {
		return false
	}
	body := strings.ToLower(e.Body)
	switch target {
	case ErrSessionNotFound:
		return e.StatusCode == http.StatusNotFound && strings.Contains(e.Path, "/v1/sessions/")
	case ErrSessionCorrupt:
		return e.StatusCode == http.StatusConflict && strings.Contains(body, "corrupt session")
	case ErrSessionReplayFailed:
		return e.StatusCode >= http.StatusInternalServerError && strings.Contains(body, "session event replay failed")
	case ErrNoSessionStore:
		return e.StatusCode == http.StatusConflict && strings.Contains(body, "no session store")
	default:
		return false
	}
}

func (e *RunFailedError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return "gateway run failed"
	}
	return e.Message
}

func (e *EventSeqGapError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("gateway event sequence gap: got seq %d after %d", e.GotSeq, e.AfterSeq)
}

func (c *Client) CreateSession(ctx context.Context, profile string) (string, error) {
	return c.CreateSessionFromMessages(ctx, profile, nil)
}

func (c *Client) CreateSessionFromMessages(ctx context.Context, profile string, messages []protocol.Message) (string, error) {
	return c.CreateSessionFromMessagesWithOwner(ctx, profile, messages, gatewayapi.SessionOwner{})
}

func (c *Client) CreateSessionWithOwner(ctx context.Context, profile string, owner gatewayapi.SessionOwner) (string, error) {
	return c.CreateSessionFromMessagesWithOwner(ctx, profile, nil, owner)
}

func (c *Client) CreateSessionFromMessagesWithOwner(ctx context.Context, profile string, messages []protocol.Message, owner gatewayapi.SessionOwner) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	req := gatewayapi.CreateSessionRequest{Profile: profile, Messages: messages, Owner: owner}
	if err := c.JSON(ctx, http.MethodPost, "/v1/sessions", req, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("gateway returned empty session id")
	}
	return out.ID, nil
}

func (c *Client) ListSessions(ctx context.Context) ([]gatewayapi.SessionSummary, error) {
	var out gatewayapi.SessionListResponse
	if err := c.JSON(ctx, http.MethodGet, "/v1/sessions", nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (gatewayapi.SessionResponse, error) {
	var out gatewayapi.SessionResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID))
	if err := c.JSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return gatewayapi.SessionResponse{}, err
	}
	return out, nil
}

func (c *Client) SessionStatus(ctx context.Context, sessionID string) (gatewayapi.SessionStatus, error) {
	var out gatewayapi.SessionStatus
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/status"
	if err := c.JSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return gatewayapi.SessionStatus{}, err
	}
	return out, nil
}

func (c *Client) SessionInspectRaw(ctx context.Context, sessionID string) (json.RawMessage, error) {
	var out json.RawMessage
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/inspect"
	if err := c.JSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) RunSession(ctx context.Context, sessionID string, run gatewayapi.RunRequest, emit func(protocol.Event)) error {
	_, err := c.RunSessionResult(ctx, sessionID, run, emit)
	return err
}

func (c *Client) RunSessionResult(ctx context.Context, sessionID string, run gatewayapi.RunRequest, emit func(protocol.Event)) (RunResult, error) {
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/run"
	body, err := json.Marshal(run)
	if err != nil {
		return RunResult{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return RunResult{}, err
	}
	defer resp.Body.Close()
	if err := statusError(resp, http.MethodPost, path); err != nil {
		return RunResult{}, err
	}
	result, err := decodeEvents(resp.Body, 0, emit)
	if err != nil {
		return result, err
	}
	if result.Failed {
		return result, &RunFailedError{Message: result.Failure, Event: result.TerminalEvent}
	}
	return result, nil
}

func (c *Client) ReplaySessionEvents(ctx context.Context, sessionID string, afterSeq int64, emit func(protocol.Event)) error {
	return c.sessionEvents(ctx, sessionID, afterSeq, false, emit)
}

func (c *Client) FollowSessionEvents(ctx context.Context, sessionID string, afterSeq int64, emit func(protocol.Event)) error {
	return c.sessionEvents(ctx, sessionID, afterSeq, true, emit)
}

func (c *Client) AdmitSessionInput(ctx context.Context, sessionID string, input gatewayapi.SessionInputRequest) (gatewayapi.SessionInputResponse, error) {
	var out gatewayapi.SessionInputResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/inputs"
	if err := c.JSON(ctx, http.MethodPost, path, input, &out); err != nil {
		return gatewayapi.SessionInputResponse{}, err
	}
	return out, nil
}

func (c *Client) AgentClubCapabilities(ctx context.Context) (agentclub.CapabilityListResponse, error) {
	var out agentclub.CapabilityListResponse
	if err := c.JSON(ctx, http.MethodGet, "/v1/agentclub/capabilities", nil, &out); err != nil {
		return agentclub.CapabilityListResponse{}, err
	}
	return out, nil
}

func (c *Client) CreateAgentClubProposal(ctx context.Context, sessionID string, proposal agentclub.ProposalCreateRequest) (agentclub.ProposalCreateResponse, error) {
	var out agentclub.ProposalCreateResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/agentclub/proposals"
	if err := c.JSON(ctx, http.MethodPost, path, proposal, &out); err != nil {
		return agentclub.ProposalCreateResponse{}, err
	}
	return out, nil
}

func (c *Client) AgentClubProposals(ctx context.Context, sessionID string) (agentclub.ProposalListResponse, error) {
	var out agentclub.ProposalListResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/agentclub/proposals"
	if err := c.JSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return agentclub.ProposalListResponse{}, err
	}
	return out, nil
}

func (c *Client) DecideAgentClubProposal(ctx context.Context, sessionID, proposalID string, decision agentclub.ProposalDecisionRequest) (agentclub.ProposalDecisionResponse, error) {
	var out agentclub.ProposalDecisionResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/agentclub/proposals/" + url.PathEscape(strings.TrimSpace(proposalID)) + "/decision"
	if err := c.JSON(ctx, http.MethodPost, path, decision, &out); err != nil {
		return agentclub.ProposalDecisionResponse{}, err
	}
	return out, nil
}

func (c *Client) CompleteSessionInput(ctx context.Context, sessionID, inputID string, input gatewayapi.SessionInputCompleteRequest) (gatewayapi.SessionInputResponse, error) {
	var out gatewayapi.SessionInputResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/inputs/" + url.PathEscape(strings.TrimSpace(inputID)) + "/complete"
	if err := c.JSON(ctx, http.MethodPost, path, input, &out); err != nil {
		return gatewayapi.SessionInputResponse{}, err
	}
	return out, nil
}

func (c *Client) sessionEvents(ctx context.Context, sessionID string, afterSeq int64, follow bool, emit func(protocol.Event)) error {
	path := fmt.Sprintf("/v1/sessions/%s/events?after_seq=%d&follow=%t", url.PathEscape(strings.TrimSpace(sessionID)), afterSeq, follow)
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := statusError(resp, http.MethodGet, path); err != nil {
		return err
	}
	_, err = decodeEvents(resp.Body, afterSeq, emit)
	return err
}

func (c *Client) CancelSession(ctx context.Context, sessionID string) (bool, error) {
	var out gatewayapi.CancelSessionResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/cancel"
	if err := c.JSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return false, err
	}
	return out.Cancelled, nil
}

func (c *Client) AnswerUserInput(ctx context.Context, sessionID, requestID string, answer gatewayapi.UserInputAnswerRequest) (gatewayapi.UserInputResponse, error) {
	var out gatewayapi.UserInputResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/user_input/" + url.PathEscape(strings.TrimSpace(requestID)) + "/answer"
	if err := c.JSON(ctx, http.MethodPost, path, answer, &out); err != nil {
		return gatewayapi.UserInputResponse{}, err
	}
	return out, nil
}

func (c *Client) RejectUserInput(ctx context.Context, sessionID, requestID string, reject gatewayapi.UserInputRejectRequest) (gatewayapi.UserInputResponse, error) {
	var out gatewayapi.UserInputResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/user_input/" + url.PathEscape(strings.TrimSpace(requestID)) + "/reject"
	if err := c.JSON(ctx, http.MethodPost, path, reject, &out); err != nil {
		return gatewayapi.UserInputResponse{}, err
	}
	return out, nil
}

func (c *Client) PreviewSessionUndo(ctx context.Context, sessionID, changeID string) (gatewayapi.SessionUndoResponse, error) {
	return c.UndoSession(ctx, sessionID, gatewayapi.SessionUndoRequest{ChangeID: changeID, Preview: true})
}

func (c *Client) UndoSession(ctx context.Context, sessionID string, undo gatewayapi.SessionUndoRequest) (gatewayapi.SessionUndoResponse, error) {
	var out gatewayapi.SessionUndoResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/undo"
	if err := c.JSON(ctx, http.MethodPost, path, undo, &out); err != nil {
		return gatewayapi.SessionUndoResponse{}, err
	}
	return out, nil
}

func (c *Client) RedoSession(ctx context.Context, sessionID string) (gatewayapi.SessionUndoResponse, error) {
	var out gatewayapi.SessionUndoResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/redo"
	if err := c.JSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return gatewayapi.SessionUndoResponse{}, err
	}
	return out, nil
}

func (c *Client) Do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	return c.do(ctx, method, path, body)
}

func (c *Client) JSON(ctx context.Context, method, path string, body any, out any) error {
	var bytesBody []byte
	var err error
	if body != nil {
		bytesBody, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	resp, err := c.do(ctx, method, path, bytesBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := statusError(resp, method, path); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	baseURL := gatewayapi.NormalizeBaseURL(c.BaseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("gateway URL is empty")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return gatewayapi.DoWithReadyRetry(ctx, c.client(), baseURL, func() (*http.Request, error) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if owner, ok := SessionOwnerFromContext(ctx); ok {
			setSessionOwnerHeaders(req, owner)
		}
		gatewayapi.SetAuthHeaderFromEnv(req)
		return req, nil
	})
}

func setSessionOwnerHeaders(req *http.Request, owner gatewayapi.SessionOwner) {
	owner = normalizeSessionOwner(owner)
	if owner.ClientID != "" {
		req.Header.Set(gatewayapi.HeaderSessionClientID, owner.ClientID)
	}
	if owner.ClientType != "" {
		req.Header.Set(gatewayapi.HeaderSessionClientType, owner.ClientType)
	}
	if owner.TelegramChatID != 0 {
		req.Header.Set(gatewayapi.HeaderSessionTelegramChatID, fmt.Sprintf("%d", owner.TelegramChatID))
	}
	if owner.TelegramThreadID != 0 {
		req.Header.Set(gatewayapi.HeaderSessionTelegramThreadID, fmt.Sprintf("%d", owner.TelegramThreadID))
	}
	if owner.TelegramUserID != 0 {
		req.Header.Set(gatewayapi.HeaderSessionTelegramUserID, fmt.Sprintf("%d", owner.TelegramUserID))
	}
	if owner.TUIChatID != "" {
		req.Header.Set(gatewayapi.HeaderSessionTUIChatID, owner.TUIChatID)
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

func (c *Client) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	c.Client = &http.Client{Timeout: 0}
	return c.Client
}

func FormatSessionContext(resp gatewayapi.SessionContextResponse) string {
	var b strings.Builder
	if resp.ID != "" {
		fmt.Fprintf(&b, "session: %s\n", resp.ID)
	}
	fmt.Fprintf(&b, "messages: %d\n", resp.MessageCount)
	if resp.AttachmentCount > 0 || resp.ImageSubmissions > 0 {
		fmt.Fprintf(&b, "attachments: %d", resp.AttachmentCount)
		if resp.ImageSubmissions > 0 {
			fmt.Fprintf(&b, " image_submissions=%d", resp.ImageSubmissions)
		}
		b.WriteByte('\n')
	}
	if resp.ContextWindowTokens > 0 {
		source := contextWindowSourceSuffix(resp.ContextWindowSource)
		fmt.Fprintf(&b, "active context: %s / %s (%s%s)\n", compactContextNumber(resp.EstimatedTokens), compactContextNumber(resp.ContextWindowTokens), displayfmt.FixedPercentValue(resp.PercentUsed, 1), source)
	} else {
		fmt.Fprintf(&b, "active context: %s\n", compactContextNumber(resp.EstimatedTokens))
	}
	if resp.ContextCompactTokens > 0 {
		state := "below"
		if resp.OverCompactThreshold {
			state = "over"
		}
		fmt.Fprintf(&b, "compact threshold: %s (%s, %s)\n", compactContextNumber(resp.ContextCompactTokens), displayfmt.FixedPercentValue(resp.CompactThresholdPercent, 1), state)
	}
	if runtimeText := formatContextRuntime(resp.Runtime); runtimeText != "" {
		fmt.Fprintf(&b, "runtime: %s\n", runtimeText)
	}
	if resp.LastCompaction != nil {
		compaction := resp.LastCompaction
		label := firstNonEmpty(compaction.CompactionID, "unknown")
		if compaction.Seq > 0 {
			label += fmt.Sprintf(" seq=%d", compaction.Seq)
		}
		if compaction.ContextEpoch > 0 {
			label += fmt.Sprintf(" epoch=%d", compaction.ContextEpoch)
		}
		fmt.Fprintf(&b, "last compaction: %s", label)
		if compaction.Strategy != "" {
			fmt.Fprintf(&b, " strategy=%s", compaction.Strategy)
		}
		if compaction.BeforeTokens > 0 || compaction.AfterTokens > 0 {
			fmt.Fprintf(&b, " tokens %s→%s", compactContextNumber(compaction.BeforeTokens), compactContextNumber(compaction.AfterTokens))
		}
		if compaction.Reason != "" {
			fmt.Fprintf(&b, " reason=%s", compaction.Reason)
		}
		if compaction.PostHistoryHash != "" {
			fmt.Fprintf(&b, " post_hash=%s", shortContextHash(compaction.PostHistoryHash))
		}
		b.WriteByte('\n')
	}
	if usageText := formatContextUsage(resp.Usage); usageText != "" {
		b.WriteString(usageText)
	}
	if providerCacheExceedsActiveContext(resp.Usage, resp.EstimatedTokens) {
		b.WriteString("note: provider cache counters are billing/cache accounting, not active context.\n")
	}
	if promptText := formatContextPrompt(resp.Prompt); promptText != "" {
		b.WriteString(promptText)
	}
	if memoryText := formatContextMemory(resp.Memory); memoryText != "" {
		b.WriteString(memoryText)
	}
	if epochText := formatContextEpoch(resp.ContextEpoch, resp.ContextDrift); epochText != "" {
		b.WriteString(epochText)
	}
	if diagnosticsText := formatContextDiagnostics(resp.Diagnostics); diagnosticsText != "" {
		b.WriteString(diagnosticsText)
	}
	if resp.OutputRefs.Count > 0 || resp.OutputRefs.LargeInlineCount > 0 {
		fmt.Fprintf(&b, "output refs: %d", resp.OutputRefs.Count)
		if resp.OutputRefs.SourceBucketCount > 0 {
			fmt.Fprintf(&b, " buckets=%d", resp.OutputRefs.SourceBucketCount)
		}
		if resp.OutputRefs.LargeInlineCount > 0 {
			fmt.Fprintf(&b, " large_inline=%d", resp.OutputRefs.LargeInlineCount)
		}
		b.WriteByte('\n')
	}
	if len(resp.Thresholds) > 0 {
		var parts []string
		for _, threshold := range resp.Thresholds {
			marker := "○"
			if threshold.Crossed {
				marker = "●"
			}
			parts = append(parts, fmt.Sprintf("%s%d%%", marker, threshold.Percent))
		}
		fmt.Fprintf(&b, "thresholds: %s\n", strings.Join(parts, " "))
	}
	if len(resp.Sources) > 0 {
		b.WriteString("\nsources:\n")
		for _, source := range resp.Sources {
			var flags []string
			if source.LargeInlineCount > 0 {
				flags = append(flags, fmt.Sprintf("large inline %d", source.LargeInlineCount))
			}
			if source.OutputRefCount > 0 {
				flags = append(flags, fmt.Sprintf("output_ref %d", source.OutputRefCount))
			}
			flagText := ""
			if len(flags) > 0 {
				flagText = ", " + strings.Join(flags, ", ")
			}
			fmt.Fprintf(&b, "  %s: %s (%s, %d msg%s)\n", source.Source, compactContextNumber(source.EstimatedTokens), displayfmt.FixedPercentValue(source.Percent, 1), source.MessageCount, flagText)
		}
	}
	if len(resp.TopContributors) > 0 {
		b.WriteString("\ntop contributors:\n")
		for _, contributor := range resp.TopContributors {
			name := contributor.Source
			if contributor.Name != "" {
				name += "/" + contributor.Name
			}
			preview := contributor.Preview
			if preview == "" {
				preview = "(no text)"
			}
			var flags []string
			if contributor.LargeInline {
				if contributor.InlineBudgetBytes > 0 {
					flags = append(flags, "large inline>"+compactByteNumber(int64(contributor.InlineBudgetBytes)))
				} else {
					flags = append(flags, "large inline")
				}
			}
			if contributor.HasOutputRef {
				flags = append(flags, "output_ref")
			}
			flagText := ""
			if len(flags) > 0 {
				flagText = " [" + strings.Join(flags, ", ") + "]"
			}
			fmt.Fprintf(&b, "  #%d %s %s: %s%s - %s\n", contributor.Index, contributor.Role, name, compactContextNumber(contributor.EstimatedTokens), flagText, preview)
		}
	}
	if len(resp.Warnings) > 0 {
		b.WriteString("\nwarnings:\n")
		for _, warning := range resp.Warnings {
			if strings.TrimSpace(warning) != "" {
				fmt.Fprintf(&b, "  %s\n", strings.TrimSpace(warning))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func contextWindowSourceSuffix(source string) string {
	switch strings.TrimSpace(source) {
	case "override":
		return ", override"
	case "fallback":
		return ", fallback"
	default:
		return ""
	}
}

func formatContextRuntime(runtime gatewayapi.ContextRuntime) string {
	var parts []string
	if runtime.Model != "" {
		parts = append(parts, "model="+runtime.Model)
	}
	if runtime.Provider != "" {
		parts = append(parts, "provider="+runtime.Provider)
	}
	if runtime.ReasoningMode != "" {
		parts = append(parts, "reasoning="+runtime.ReasoningMode)
	}
	if runtime.AccessMode != "" {
		parts = append(parts, "access="+runtime.AccessMode)
	}
	if runtime.Profile != "" {
		parts = append(parts, "profile="+runtime.Profile)
	}
	return strings.Join(parts, " ")
}

func formatContextUsage(usage gatewayapi.ContextUsage) string {
	var b strings.Builder
	if usage.ModelCalls > 0 || usage.ToolCalls > 0 {
		fmt.Fprintf(&b, "activity: model_calls=%d tools=%d\n", usage.ModelCalls, usage.ToolCalls)
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.ReasoningTokens > 0 {
		fmt.Fprintf(&b, "provider usage: input=%s output=%s reasoning=%s\n", compactContextNumber(usage.InputTokens), compactContextNumber(usage.OutputTokens), compactContextNumber(usage.ReasoningTokens))
	}
	if usage.CacheHitTokens > 0 || usage.CacheMissTokens > 0 || usage.LastCacheHitTokens > 0 || usage.LastCacheMissTokens > 0 {
		fmt.Fprintf(&b, "provider cache: hit=%s miss=%s last_hit=%s last_miss=%s\n",
			compactContextNumber(usage.CacheHitTokens),
			compactContextNumber(usage.CacheMissTokens),
			compactContextNumber(usage.LastCacheHitTokens),
			compactContextNumber(usage.LastCacheMissTokens),
		)
	}
	if usage.WebSummaryInputTokens > 0 || usage.WebSummaryOutputTokens > 0 || usage.HelperModelAPITokens > 0 || usage.HelperModelInputTokens > 0 || usage.HelperModelOutputTokens > 0 || usage.HelperModelCacheHit > 0 || usage.HelperModelCacheMiss > 0 || usage.HelperAPICalls > 0 || usage.HelperCostUSD > 0 {
		fmt.Fprintf(&b, "helper usage: websum=%s→%s helper=%s→%s sumapi=%s",
			compactContextNumber(usage.WebSummaryInputTokens),
			compactContextNumber(usage.WebSummaryOutputTokens),
			compactContextNumber(usage.HelperModelInputTokens),
			compactContextNumber(usage.HelperModelOutputTokens),
			compactContextNumber(usage.HelperModelAPITokens),
		)
		if usage.HelperAPICalls > 0 {
			fmt.Fprintf(&b, " helper API calls=%d", usage.HelperAPICalls)
		}
		if usage.HelperCostUSD > 0 {
			fmt.Fprintf(&b, " helper API cost=$%.6f", usage.HelperCostUSD)
		}
		if usage.HelperModelCacheHit > 0 || usage.HelperModelCacheMiss > 0 {
			fmt.Fprintf(&b, " helper_cache_hit=%s helper_cache_miss=%s",
				compactContextNumber(usage.HelperModelCacheHit),
				compactContextNumber(usage.HelperModelCacheMiss),
			)
		}
		if usage.HelperModelCalls > 0 {
			fmt.Fprintf(&b, " helper_calls=%d", usage.HelperModelCalls)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func providerCacheExceedsActiveContext(usage gatewayapi.ContextUsage, activeContext int64) bool {
	if activeContext <= 0 {
		return false
	}
	for _, value := range []int64{
		usage.CacheHitTokens,
		usage.CacheMissTokens,
		usage.LastCacheHitTokens,
		usage.LastCacheMissTokens,
	} {
		if value > activeContext {
			return true
		}
	}
	return false
}

func formatContextPrompt(prompt gatewayapi.ContextPrompt) string {
	if contextPromptEmpty(prompt) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "prompt sections: %d sections %s tokens %s bytes", prompt.SectionCount, compactContextNumber(int64(prompt.ApproxTokens)), compactByteNumber(int64(prompt.TotalBytes)))
	if prompt.ToolSchemas > 0 {
		fmt.Fprintf(&b, " tool_schemas=%d", prompt.ToolSchemas)
	}
	if prompt.InventoryHash != "" {
		fmt.Fprintf(&b, " hash=%s", shortContextHash(prompt.InventoryHash))
	}
	b.WriteByte('\n')
	if prompt.CacheStatus != "" || prompt.CacheReason != "" {
		fmt.Fprintf(&b, "prompt cache: status=%s reason=%s\n", firstNonEmpty(prompt.CacheStatus, "unknown"), firstNonEmpty(prompt.CacheReason, "unknown"))
	}
	if len(prompt.Sections) > 0 {
		b.WriteString("prompt section budget:\n")
		limit := len(prompt.Sections)
		if limit > 6 {
			limit = 6
		}
		for _, section := range prompt.Sections[:limit] {
			fmt.Fprintf(&b, "  %s: %s tokens %s bytes hash=%s\n",
				section.Name,
				compactContextNumber(int64(section.ApproxTokens)),
				compactByteNumber(int64(section.ByteCount)),
				shortContextHash(section.SHA256),
			)
		}
		if len(prompt.Sections) > limit {
			fmt.Fprintf(&b, "  ... +%d sections\n", len(prompt.Sections)-limit)
		}
	}
	return b.String()
}

func contextPromptEmpty(prompt gatewayapi.ContextPrompt) bool {
	return prompt.InventoryHash == "" &&
		prompt.SectionCount == 0 &&
		prompt.TotalBytes == 0 &&
		prompt.ApproxTokens == 0 &&
		prompt.ToolSchemas == 0 &&
		len(prompt.Sections) == 0 &&
		prompt.CacheStatus == "" &&
		prompt.CacheReason == ""
}

func formatContextMemory(memory gatewayapi.ContextMemory) string {
	if contextMemoryEmpty(memory) {
		return ""
	}
	var parts []string
	if memory.Status != "" {
		parts = append(parts, "status="+memory.Status)
	}
	if memory.Policy != "" {
		parts = append(parts, "policy="+memory.Policy)
	}
	if memory.LockedHash != "" {
		parts = append(parts, "locked="+shortContextHash(memory.LockedHash))
	}
	if memory.CurrentHash != "" {
		parts = append(parts, "current="+shortContextHash(memory.CurrentHash))
	}
	if memory.CurrentEntries > 0 {
		parts = append(parts, fmt.Sprintf("entries=%d", memory.CurrentEntries))
	}
	if memory.CurrentRoots > 0 {
		parts = append(parts, fmt.Sprintf("roots=%d", memory.CurrentRoots))
	}
	if memory.CurrentWarnings > 0 {
		parts = append(parts, fmt.Sprintf("warnings=%d", memory.CurrentWarnings))
	}
	if memory.CurrentCapped {
		parts = append(parts, "capped=true")
	}
	if memory.CurrentError != "" {
		parts = append(parts, "error="+memory.CurrentError)
	}
	if len(parts) == 0 {
		return ""
	}
	return "memory: " + strings.Join(parts, " ") + "\n"
}

func contextMemoryEmpty(memory gatewayapi.ContextMemory) bool {
	return memory == (gatewayapi.ContextMemory{})
}

func formatContextEpoch(epoch *protocol.ContextEpoch, drift *protocol.ContextEpochDrift) string {
	if epoch == nil && drift == nil {
		return ""
	}
	var parts []string
	if drift != nil && drift.Status != "" {
		parts = append(parts, "status="+drift.Status)
	}
	if epoch != nil && epoch.Policy != "" {
		parts = append(parts, "policy="+epoch.Policy)
	} else if drift != nil && drift.Policy != "" {
		parts = append(parts, "policy="+drift.Policy)
	}
	if epoch != nil && epoch.Hash != "" {
		parts = append(parts, "run="+shortContextHash(epoch.Hash))
	}
	if drift != nil && drift.LockedHash != "" {
		parts = append(parts, "locked="+shortContextHash(drift.LockedHash))
	}
	if drift != nil && drift.CurrentHash != "" {
		parts = append(parts, "current="+shortContextHash(drift.CurrentHash))
	}
	if epoch != nil && epoch.AgentsHash != "" {
		parts = append(parts, "agents="+shortContextHash(epoch.AgentsHash))
	}
	if epoch != nil && epoch.MemoryHash != "" {
		parts = append(parts, "memory="+shortContextHash(epoch.MemoryHash))
	}
	if epoch != nil && epoch.ProjectContextHash != "" {
		parts = append(parts, "project="+shortContextHash(epoch.ProjectContextHash))
	}
	if epoch != nil && epoch.DocsIndexHash != "" {
		parts = append(parts, "docs="+shortContextHash(epoch.DocsIndexHash))
	}
	if drift != nil && len(drift.ChangedFields) > 0 {
		parts = append(parts, "changed="+strings.Join(drift.ChangedFields, ","))
	}
	if len(parts) == 0 {
		return ""
	}
	return "context epoch: " + strings.Join(parts, " ") + "\n"
}

func formatContextDiagnostics(diagnostics gatewayapi.ContextDiagnostics) string {
	if contextDiagnosticsEmpty(diagnostics) {
		return ""
	}
	var parts []string
	if diagnostics.CurrentEpoch > 0 {
		parts = append(parts, fmt.Sprintf("epoch=%d", diagnostics.CurrentEpoch))
	}
	if diagnostics.ContextEpochHash != "" {
		parts = append(parts, "context_epoch="+shortContextHash(diagnostics.ContextEpochHash))
	}
	if diagnostics.ContextEpochStatus != "" {
		parts = append(parts, "context_epoch_status="+diagnostics.ContextEpochStatus)
	}
	if diagnostics.ConfigHash != "" {
		parts = append(parts, "config_hash="+shortContextHash(diagnostics.ConfigHash))
	}
	if diagnostics.ToolCatalogHash != "" {
		parts = append(parts, "tool_catalog="+shortContextHash(diagnostics.ToolCatalogHash))
	}
	if diagnostics.MCPCatalogHash != "" {
		parts = append(parts, "mcp_catalog="+shortContextHash(diagnostics.MCPCatalogHash))
	}
	if diagnostics.DocsIndexHash != "" {
		parts = append(parts, "docs_hash="+shortContextHash(diagnostics.DocsIndexHash))
	}
	if diagnostics.CompactionEvents > 0 {
		parts = append(parts, fmt.Sprintf("compactions=%d", diagnostics.CompactionEvents))
	}
	if diagnostics.ThresholdEvents > 0 {
		parts = append(parts, fmt.Sprintf("thresholds=%d", diagnostics.ThresholdEvents))
	}
	if diagnostics.ToolCallEvents > 0 {
		parts = append(parts, fmt.Sprintf("tools=%d", diagnostics.ToolCallEvents))
	}
	if diagnostics.HelperModelCalls > 0 {
		parts = append(parts, fmt.Sprintf("helper_calls=%d", diagnostics.HelperModelCalls))
	}
	if diagnostics.ProtectedPrefixTokens > 0 {
		parts = append(parts, "protected="+compactContextNumber(diagnostics.ProtectedPrefixTokens))
	}
	if diagnostics.BodyTokens > 0 {
		parts = append(parts, "body="+compactContextNumber(diagnostics.BodyTokens))
	}
	if diagnostics.WindowRemainingTokens != 0 {
		parts = append(parts, "window_remaining="+compactContextNumber(diagnostics.WindowRemainingTokens))
	}
	if diagnostics.CompactMarginTokens != 0 {
		parts = append(parts, "compact_margin="+compactContextNumber(diagnostics.CompactMarginTokens))
	}
	if diagnostics.MemoryContextHash != "" {
		parts = append(parts, "memory_hash="+shortContextHash(diagnostics.MemoryContextHash))
	}
	if diagnostics.ProjectContextHash != "" {
		parts = append(parts, "project_hash="+shortContextHash(diagnostics.ProjectContextHash))
	}
	if diagnostics.AgentsInstructionsHash != "" {
		parts = append(parts, "agents_hash="+shortContextHash(diagnostics.AgentsInstructionsHash))
	}
	if diagnostics.MCPInstructionsHash != "" {
		parts = append(parts, "mcp_hash="+shortContextHash(diagnostics.MCPInstructionsHash))
	}
	if diagnostics.PromptInventoryHash != "" {
		parts = append(parts, "prompt_hash="+shortContextHash(diagnostics.PromptInventoryHash))
	}
	if diagnostics.LastCompactionHistoryHash != "" {
		parts = append(parts, "post_hash="+shortContextHash(diagnostics.LastCompactionHistoryHash))
	}
	if len(parts) == 0 {
		return ""
	}
	return "diagnostics: " + strings.Join(parts, " ") + "\n"
}

func contextDiagnosticsEmpty(diagnostics gatewayapi.ContextDiagnostics) bool {
	return diagnostics == (gatewayapi.ContextDiagnostics{})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func shortContextHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func statusError(resp *http.Response, method, path string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &StatusError{
		Method:     method,
		Path:       path,
		StatusCode: resp.StatusCode,
		Body:       string(limited),
	}
}

func decodeEvents(reader io.Reader, cursor int64, emit func(protocol.Event)) (RunResult, error) {
	var result RunResult
	dec := json.NewDecoder(reader)
	for {
		var event protocol.Event
		if err := dec.Decode(&event); err != nil {
			if err == io.EOF {
				return result, nil
			}
			return result, err
		}
		if event.Seq > 0 && event.Seq <= cursor {
			continue
		}
		if event.Seq > 0 && cursor > 0 && event.Seq > cursor+1 {
			result.SeqGap = &EventSeqGapError{AfterSeq: cursor, GotSeq: event.Seq}
			return result, result.SeqGap
		}
		if event.Seq > 0 {
			cursor = event.Seq
			result.LastSeq = event.Seq
		}
		result.EventCount++
		if event.Type == protocol.EventGatewayStreamGap {
			result.StreamGaps++
			result.DroppedEvents += streamGapDroppedEvents(event.Data)
		}
		switch event.Type {
		case protocol.EventRunCompleted:
			result.Completed = true
			result.Failed = false
			result.Failure = ""
			result.TerminalEvent = event
		case protocol.EventRunFailed:
			result.Completed = false
			result.Failed = true
			result.Failure = fmt.Sprint(event.Data)
			result.TerminalEvent = event
		}
		if emit != nil {
			emit(event)
		}
	}
}

func streamGapDroppedEvents(data any) int64 {
	switch value := data.(type) {
	case protocol.GatewayStreamGapEvent:
		return value.DroppedEvents
	case map[string]any:
		switch raw := value["dropped_events"].(type) {
		case float64:
			return int64(raw)
		case int64:
			return raw
		case json.Number:
			n, _ := raw.Int64()
			return n
		}
	}
	return 0
}

func compactContextNumber(value int64) string {
	return displayfmt.CompactContext(value)
}

func compactByteNumber(value int64) string {
	return compactContextNumber(value) + "B"
}
