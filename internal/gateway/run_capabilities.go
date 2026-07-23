package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

const maxRunRequestBodyBytes int64 = 1 << 20

var errRunRequestBodyTooLarge = errors.New("run request body too large")

type runRequestDecodeInfo struct {
	capabilityFieldsPresent bool
	duplicateField          string
	nonCanonicalField       string
}

func decodeRunRequest(body io.Reader) (RunRequest, runRequestDecodeInfo, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return RunRequest{}, runRequestDecodeInfo{}, err
	}
	var req RunRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&req); err != nil {
		return RunRequest{}, runRequestDecodeInfo{}, err
	}
	info, err := inspectRunRequestJSON(raw)
	if err != nil {
		return RunRequest{}, runRequestDecodeInfo{}, err
	}
	constrained := isConstrainedRunAccessMode(req.AccessMode) || info.capabilityFieldsPresent
	if !constrained {
		return req, info, nil
	}
	if int64(len(raw)) > maxRunRequestBodyBytes {
		return RunRequest{}, runRequestDecodeInfo{}, fmt.Errorf("%w: maximum is %d bytes", errRunRequestBodyTooLarge, maxRunRequestBodyBytes)
	}
	if info.nonCanonicalField != "" {
		return RunRequest{}, runRequestDecodeInfo{}, fmt.Errorf(
			"non-canonical JSON field %q is not allowed in a constrained run request",
			info.nonCanonicalField,
		)
	}
	if info.duplicateField != "" {
		return RunRequest{}, runRequestDecodeInfo{}, fmt.Errorf("duplicate JSON field %q is not allowed in a constrained run request", info.duplicateField)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return RunRequest{}, runRequestDecodeInfo{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return RunRequest{}, runRequestDecodeInfo{}, fmt.Errorf("request body must contain exactly one JSON object")
		}
		return RunRequest{}, runRequestDecodeInfo{}, fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return req, info, nil
}

func isConstrainedRunAccessMode(value string) bool {
	switch strings.TrimSpace(value) {
	case gatewayapi.AccessModeIsolatedPlanV1,
		gatewayapi.AccessModeBoundedIsolatedPlanV1,
		gatewayapi.AccessModeBoundedAutomationV1:
		return true
	default:
		return false
	}
}

func inspectRunRequestJSON(raw []byte) (runRequestDecodeInfo, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return runRequestDecodeInfo{}, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return runRequestDecodeInfo{}, fmt.Errorf("run request body must be a JSON object")
	}
	seen := map[string]struct{}{}
	var info runRequestDecodeInfo
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return runRequestDecodeInfo{}, err
		}
		key, ok := token.(string)
		if !ok {
			return runRequestDecodeInfo{}, fmt.Errorf("run request field name must be a string")
		}
		canonicalKey := canonicalRunRequestJSONField(key)
		if canonicalKey != "" && canonicalKey != key && info.nonCanonicalField == "" {
			info.nonCanonicalField = key
		}
		identity := key
		if canonicalKey != "" {
			identity = canonicalKey
		}
		if _, exists := seen[identity]; exists && info.duplicateField == "" {
			info.duplicateField = identity
		}
		seen[identity] = struct{}{}
		switch canonicalKey {
		case "context_mode", "allowed_tools", "allowed_url_prefixes":
			info.capabilityFieldsPresent = true
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return runRequestDecodeInfo{}, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return runRequestDecodeInfo{}, err
	}
	return info, nil
}

func canonicalRunRequestJSONField(key string) string {
	for _, canonical := range []string{
		"prompt",
		"attachments",
		"input_id",
		"client_id",
		"client_type",
		"provider",
		"model",
		"profile",
		"thinking",
		"reasoning_effort",
		"max_tool_rounds",
		"max_tool_calls",
		"access_mode",
		"context_mode",
		"allowed_tools",
		"allowed_url_prefixes",
		"interrupt_policy",
		"metadata",
	} {
		if strings.EqualFold(key, canonical) {
			return canonical
		}
	}
	return ""
}

func (s *Server) runCapabilitiesForRequest(req RunRequest) (string, tools.RunCapabilities, error) {
	return s.runCapabilitiesForRequestWithPresence(
		req,
		req.ContextMode != "" || len(req.AllowedTools) > 0 || len(req.AllowedURLPrefixes) > 0,
	)
}

func (s *Server) runCapabilitiesForRequestWithPresence(req RunRequest, capabilityFieldsPresent bool) (string, tools.RunCapabilities, error) {
	hasCapabilityFields := capabilityFieldsPresent ||
		req.ContextMode != "" || len(req.AllowedTools) > 0 || len(req.AllowedURLPrefixes) > 0
	isolatedScope := ""
	switch req.AccessMode {
	case gatewayapi.AccessModeIsolatedPlanV1:
		isolatedScope = protocol.CapabilityScopeIsolatedPlanV1
	case gatewayapi.AccessModeBoundedIsolatedPlanV1:
		isolatedScope = protocol.CapabilityScopeBoundedIsolatedPlanV1
	}
	if !hasCapabilityFields && isolatedScope == "" {
		return "", tools.RunCapabilities{}, nil
	}
	if isolatedScope == "" {
		return "", tools.RunCapabilities{}, fmt.Errorf(
			"per-run capability fields require access_mode %q so older gateways reject the request",
			gatewayapi.AccessModeBoundedIsolatedPlanV1,
		)
	}
	if req.ContextMode != gatewayapi.ContextModeIsolated {
		return "", tools.RunCapabilities{}, fmt.Errorf(
			"access_mode %q requires context_mode %q",
			req.AccessMode,
			gatewayapi.ContextModeIsolated,
		)
	}
	if len(req.AllowedTools) == 0 {
		return "", tools.RunCapabilities{}, fmt.Errorf("access_mode %q requires a non-empty allowed_tools allowlist", req.AccessMode)
	}
	if len(req.AllowedURLPrefixes) == 0 {
		return "", tools.RunCapabilities{}, fmt.Errorf("access_mode %q requires non-empty allowed_url_prefixes", req.AccessMode)
	}
	switch {
	case req.Profile != "":
		return "", tools.RunCapabilities{}, fmt.Errorf("isolated scope does not allow profile overrides")
	case len(req.Attachments) > 0:
		return "", tools.RunCapabilities{}, fmt.Errorf("isolated scope does not allow attachment references")
	case req.Provider != "":
		return "", tools.RunCapabilities{}, fmt.Errorf("isolated scope does not allow provider overrides")
	case req.Model != "":
		return "", tools.RunCapabilities{}, fmt.Errorf("isolated scope does not allow model overrides")
	case req.Thinking != "":
		return "", tools.RunCapabilities{}, fmt.Errorf("isolated scope does not allow thinking overrides")
	case req.ReasoningEffort != "":
		return "", tools.RunCapabilities{}, fmt.Errorf("isolated scope does not allow reasoning_effort overrides")
	}
	hasWebFetcher := false
	for _, name := range req.AllowedTools {
		switch name {
		case "time_now":
		case "web_fetch", "web_extract", "web_crawl":
			hasWebFetcher = true
		default:
			return "", tools.RunCapabilities{}, fmt.Errorf(
				"allowed_tools entry %q is not available in isolated scope; allowed values are time_now, web_fetch, web_extract, and web_crawl",
				name,
			)
		}
	}
	if !hasWebFetcher {
		return "", tools.RunCapabilities{}, fmt.Errorf("isolated scope requires at least one of web_fetch, web_extract, or web_crawl")
	}
	if s == nil || s.registry == nil {
		return "", tools.RunCapabilities{}, fmt.Errorf("per-run capabilities require an available tool registry")
	}
	capabilities, err := s.registry.NewRunCapabilities(isolatedScope, req.AllowedTools, req.AllowedURLPrefixes)
	if err != nil {
		return "", tools.RunCapabilities{}, err
	}
	return protocol.ContextModeIsolated, capabilities, nil
}

func validateSessionRunCapabilityScopeWithPresence(req RunRequest, capabilityFieldsPresent bool) error {
	if capabilityFieldsPresent ||
		req.ContextMode != "" || len(req.AllowedTools) > 0 || len(req.AllowedURLPrefixes) > 0 ||
		req.AccessMode == gatewayapi.AccessModeIsolatedPlanV1 ||
		req.AccessMode == gatewayapi.AccessModeBoundedIsolatedPlanV1 {
		return fmt.Errorf("isolated per-run capabilities are supported only by POST /v1/run, not existing sessions")
	}
	return nil
}
