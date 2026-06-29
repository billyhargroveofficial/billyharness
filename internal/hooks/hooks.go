package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

type Runner struct {
	hooksByEvent map[string][]config.Hook
	loadErr      error
}

func New(settings config.HookSettings) *Runner {
	if !settings.Enabled {
		return &Runner{}
	}
	hooks := settings.Hooks
	if len(hooks) == 0 {
		files := settings.ConfigFiles
		if len(files) == 0 {
			files = config.DefaultHookConfigFiles()
		}
		loaded, err := config.LoadHooks(files)
		if err != nil {
			return &Runner{loadErr: err}
		}
		hooks = loaded
	}
	hooksByEvent := map[string][]config.Hook{}
	for _, hook := range hooks {
		if !hook.Enabled {
			continue
		}
		event := normalizeEvent(hook.Event)
		if event == "" {
			continue
		}
		hooksByEvent[event] = append(hooksByEvent[event], hook)
	}
	return &Runner{hooksByEvent: hooksByEvent}
}

func (r *Runner) Run(ctx context.Context, event string, payload map[string]any, emit func(protocol.Event)) error {
	event = normalizeEvent(event)
	if r == nil || event == "" {
		return nil
	}
	if emit == nil {
		emit = func(protocol.Event) {}
	}
	if r.loadErr != nil {
		emit(protocol.Event{Type: protocol.EventHookFailed, Data: protocol.HookEvent{
			HookEvent: event,
			Status:    protocol.StepStatusFailed,
			Error:     r.loadErr.Error(),
			Phase:     "load",
		}})
		return nil
	}
	var fatalErr error
	for _, hook := range r.hooksByEvent[event] {
		result := runOne(ctx, hook, event, payload, emit)
		if result.err != nil && hook.Fatal {
			fatalErr = errors.Join(fatalErr, result.err)
		}
	}
	return fatalErr
}

type runResult struct {
	err error
}

func runOne(ctx context.Context, hook config.Hook, event string, payload map[string]any, emit func(protocol.Event)) runResult {
	started := time.Now()
	base := hookEventData(hook, event, payload)
	emit(protocol.Event{Type: protocol.EventHookStarted, Data: base})

	timeout := hook.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input, _ := json.Marshal(map[string]any{
		"event":   event,
		"hook":    hook.Name,
		"payload": payload,
	})
	cmd := exec.CommandContext(runCtx, hook.Command, hook.Args...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = hookEnv(hook, event, payload)
	if cwd := hookCWD(hook); cwd != "" {
		cmd.Dir = cwd
	}
	outputLimit := hookOutputLimit(hook)
	stdout := &limitedBuffer{limit: hookCaptureLimit(hook)}
	stderr := &limitedBuffer{limit: hookCaptureLimit(hook)}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	duration := time.Since(started).Milliseconds()
	data := hookEventData(hook, event, payload)
	data.DurationMS = int64Ptr(duration)
	stdoutText, stdoutTruncated := hookOutput(stdout.String(), outputLimit, stdout.truncated)
	stderrText, stderrTruncated := hookOutput(stderr.String(), outputLimit, stderr.truncated)
	data.Stdout = stdoutText
	data.Stderr = stderrText
	data.StdoutTruncated = boolPtr(stdoutTruncated)
	data.StderrTruncated = boolPtr(stderrTruncated)
	data.TimeoutMS = int64Ptr(timeout.Milliseconds())
	data.ExitCode = intPtr(exitCode(err))
	if runCtx.Err() == context.DeadlineExceeded {
		data.TimedOut = boolPtr(true)
	}
	if err != nil {
		data.Status = protocol.StepStatusFailed
		data.Error = secrets.Redact(err.Error())
		emit(protocol.Event{Type: protocol.EventHookFailed, DurationMS: duration, Data: data})
		return runResult{err: fmt.Errorf("hook %s/%s failed: %w", event, hook.Name, err)}
	}
	data.Status = protocol.StepStatusCompleted
	emit(protocol.Event{Type: protocol.EventHookFinished, DurationMS: duration, Data: data})
	return runResult{}
}

func hookEventData(hook config.Hook, event string, payload map[string]any) protocol.HookEvent {
	data := protocol.HookEvent{
		HookEvent: event,
		HookName:  hook.Name,
		Name:      hook.Name,
		Command:   filepath.Base(hook.Command),
		Fatal:     hook.Fatal,
		Status:    protocol.StepStatusStarted,
	}
	if len(payload) > 0 {
		data.Payload = payload
	}
	data.TurnID = payloadString(payload, "turn_id")
	data.StepID = payloadString(payload, "step_id")
	data.CallID = payloadString(payload, "call_id")
	data.AttemptID = payloadString(payload, "attempt_id")
	data.ToolName = payloadString(payload, "tool_name")
	data.RequestID = payloadString(payload, "request_id")
	data.ProviderID = payloadString(payload, "provider_id")
	data.ModelID = payloadString(payload, "model_id")
	data.ProviderRequestID = payloadString(payload, "provider_request_id")
	data.Attempts = payloadInt(payload, "attempts")
	data.Retries = payloadInt(payload, "retries")
	data.StatusCode = payloadInt(payload, "status_code")
	data.ServerName = payloadString(payload, "server_name")
	data.Transport = payloadString(payload, "transport")
	data.Connected = payloadBool(payload, "connected")
	data.State = payloadString(payload, "state")
	data.ToolCount = payloadInt(payload, "tool_count")
	data.RetryCount = payloadInt(payload, "retry_count")
	data.RestartCount = payloadInt(payload, "restart_count")
	data.RetryBackoffMS = payloadInt64(payload, "retry_backoff_ms")
	data.ArgsSummary = payloadString(payload, "args_summary")
	data.ErrorCode = payloadString(payload, "error_code")
	data.IsError = payloadBool(payload, "is_error")
	data.DurationMS = payloadInt64(payload, "duration_ms")
	data.OutputBytes = payloadInt64(payload, "output_bytes")
	data.OutputEstimatedTokens = payloadInt64(payload, "output_estimated_tokens")
	data.Truncated = payloadBool(payload, "truncated")
	data.OutputRef = payloadString(payload, "output_ref")
	data.PermissionDecision = payloadString(payload, "permission_decision")
	data.PermissionSource = payloadString(payload, "permission_source")
	data.PermissionReason = payloadString(payload, "permission_reason")
	return data
}

func payloadString(payload map[string]any, key string) string {
	switch value := payload[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func payloadBool(payload map[string]any, key string) *bool {
	if value, ok := payload[key].(bool); ok {
		return &value
	}
	return nil
}

func payloadInt(payload map[string]any, key string) *int {
	switch value := payload[key].(type) {
	case int:
		return &value
	case int8:
		out := int(value)
		return &out
	case int16:
		out := int(value)
		return &out
	case int32:
		out := int(value)
		return &out
	case int64:
		out := int(value)
		return &out
	case uint:
		out := int(value)
		return &out
	case uint8:
		out := int(value)
		return &out
	case uint16:
		out := int(value)
		return &out
	case uint32:
		out := int(value)
		return &out
	case uint64:
		out := int(value)
		return &out
	case float64:
		out := int(value)
		return &out
	case float32:
		out := int(value)
		return &out
	default:
		return nil
	}
}

func payloadInt64(payload map[string]any, key string) *int64 {
	switch value := payload[key].(type) {
	case int:
		out := int64(value)
		return &out
	case int8:
		out := int64(value)
		return &out
	case int16:
		out := int64(value)
		return &out
	case int32:
		out := int64(value)
		return &out
	case int64:
		return &value
	case uint:
		out := int64(value)
		return &out
	case uint8:
		out := int64(value)
		return &out
	case uint16:
		out := int64(value)
		return &out
	case uint32:
		out := int64(value)
		return &out
	case uint64:
		out := int64(value)
		return &out
	case float64:
		out := int64(value)
		return &out
	case float32:
		out := int64(value)
		return &out
	default:
		return nil
	}
}

func intPtr(value int) *int {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func hookEnv(hook config.Hook, event string, payload map[string]any) []string {
	env := os.Environ()
	env = append(env,
		"BILLYHARNESS_HOOK_EVENT="+event,
		"BILLYHARNESS_HOOK_NAME="+hook.Name,
	)
	for _, key := range []string{"run_id", "turn_id", "step_id", "call_id", "attempt_id", "tool_name"} {
		if value := strings.TrimSpace(fmt.Sprint(payload[key])); value != "" && value != "<nil>" {
			env = append(env, "BILLYHARNESS_"+strings.ToUpper(key)+"="+value)
		}
	}
	for _, key := range hook.EnvVars {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	for key, value := range hook.Env {
		if strings.TrimSpace(key) != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func hookCWD(hook config.Hook) string {
	cwd := strings.TrimSpace(hook.CWD)
	if cwd == "" {
		return ""
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}

func hookOutputLimit(hook config.Hook) int {
	if hook.MaxOutputBytes <= 0 {
		return 4096
	}
	return hook.MaxOutputBytes
}

func hookCaptureLimit(hook config.Hook) int {
	limit := hookOutputLimit(hook)
	return limit + 4096
}

func hookOutput(text string, limit int, alreadyTruncated bool) (string, bool) {
	redacted := secrets.Redact(text)
	if limit <= 0 {
		return "", redacted != "" || alreadyTruncated
	}
	if len(redacted) <= limit {
		return redacted, alreadyTruncated
	}
	return trimUTF8Bytes(redacted, limit), true
}

func trimUTF8Bytes(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.ValidString(text[:limit]) {
		limit--
	}
	return text[:limit]
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func normalizeEvent(event string) string {
	event = strings.ToLower(strings.TrimSpace(event))
	event = strings.ReplaceAll(event, "-", "_")
	return strings.Trim(event, "_")
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated = true
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}
