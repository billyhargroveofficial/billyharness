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

func New(cfg config.Config) *Runner {
	if !cfg.HooksEnabled {
		return &Runner{}
	}
	if len(cfg.Hooks) == 0 {
		if err := cfg.LoadDefaultHooks(); err != nil {
			return &Runner{loadErr: err}
		}
	}
	hooksByEvent := map[string][]config.Hook{}
	for _, hook := range cfg.Hooks {
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
		emit(protocol.Event{Type: protocol.EventHookFailed, Data: map[string]any{
			"hook_event": event,
			"status":     protocol.StepStatusFailed,
			"error":      r.loadErr.Error(),
			"phase":      "load",
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
	data["duration_ms"] = duration
	stdoutText, stdoutTruncated := hookOutput(stdout.String(), outputLimit, stdout.truncated)
	stderrText, stderrTruncated := hookOutput(stderr.String(), outputLimit, stderr.truncated)
	data["stdout"] = stdoutText
	data["stderr"] = stderrText
	data["stdout_truncated"] = stdoutTruncated
	data["stderr_truncated"] = stderrTruncated
	data["timeout_ms"] = timeout.Milliseconds()
	data["exit_code"] = exitCode(err)
	if runCtx.Err() == context.DeadlineExceeded {
		data["timed_out"] = true
	}
	if err != nil {
		data["status"] = protocol.StepStatusFailed
		data["error"] = secrets.Redact(err.Error())
		emit(protocol.Event{Type: protocol.EventHookFailed, DurationMS: duration, Data: data})
		return runResult{err: fmt.Errorf("hook %s/%s failed: %w", event, hook.Name, err)}
	}
	data["status"] = protocol.StepStatusCompleted
	emit(protocol.Event{Type: protocol.EventHookFinished, DurationMS: duration, Data: data})
	return runResult{}
}

func hookEventData(hook config.Hook, event string, payload map[string]any) map[string]any {
	data := map[string]any{
		"hook_event": event,
		"hook_name":  hook.Name,
		"name":       hook.Name,
		"command":    filepath.Base(hook.Command),
		"fatal":      hook.Fatal,
		"status":     protocol.StepStatusStarted,
	}
	if len(payload) > 0 {
		data["payload"] = payload
	}
	for _, key := range []string{
		"turn_id", "step_id", "call_id", "attempt_id", "tool_name",
		"request_id", "provider_id", "model_id", "provider_request_id", "attempts", "retries", "status_code",
		"args_summary", "error_code", "is_error", "duration_ms", "output_bytes", "output_estimated_tokens",
		"truncated", "output_ref", "permission_decision", "permission_source", "permission_reason",
	} {
		if value := payload[key]; value != nil && value != "" {
			data[key] = value
		}
	}
	return data
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
