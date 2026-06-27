package bench

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
	"github.com/billyhargroveofficial/billyharness/internal/trace"
)

type Task struct {
	ID                     string     `json:"id"`
	Suite                  string     `json:"suite"`
	Prompt                 string     `json:"prompt"`
	Workspace              string     `json:"workspace,omitempty"`
	WorkspaceTemplate      string     `json:"workspace_template,omitempty"`
	Setup                  [][]string `json:"setup,omitempty"`
	Evaluator              []string   `json:"evaluator,omitempty"`
	TimeoutSeconds         int        `json:"timeout_seconds,omitempty"`
	Tags                   []string   `json:"tags,omitempty"`
	ScriptedToolRounds     int        `json:"scripted_tool_rounds,omitempty"`
	ScriptedToolName       string     `json:"scripted_tool_name,omitempty"`
	ScriptedToolArgs       string     `json:"scripted_tool_args,omitempty"`
	ContextCompactTokens   int        `json:"context_compact_tokens,omitempty"`
	ContextCompactKeep     int        `json:"context_compact_keep,omitempty"`
	ContextCompactMaxChars int        `json:"context_compact_max_chars,omitempty"`
}

type RunConfig struct {
	TasksPath              string
	OutDir                 string
	Limit                  int
	Mock                   bool
	Model                  string
	Timeout                time.Duration
	ScriptedToolRounds     int
	ContextCompactTokens   int
	ContextCompactKeep     int
	ContextCompactMaxChars int
}

type Result struct {
	RunID                 string         `json:"run_id"`
	TaskID                string         `json:"task_id"`
	TaskSuite             string         `json:"task_suite"`
	Model                 string         `json:"model"`
	Harness               string         `json:"harness"`
	PromptHash            string         `json:"prompt_hash"`
	Workspace             string         `json:"workspace,omitempty"`
	Tags                  []string       `json:"tags,omitempty"`
	Outcome               string         `json:"outcome"`
	WallTimeMS            int64          `json:"wall_time_ms"`
	ModelCalls            int            `json:"model_calls"`
	ToolCalls             int            `json:"tool_calls"`
	ToolCallsByName       map[string]int `json:"tool_calls_by_name,omitempty"`
	ToolErrors            int            `json:"tool_errors"`
	InputTokens           int64          `json:"input_tokens,omitempty"`
	OutputTokens          int64          `json:"output_tokens,omitempty"`
	CacheHitTokens        int64          `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens       int64          `json:"cache_miss_tokens,omitempty"`
	ContextCompactions    int            `json:"context_compactions,omitempty"`
	ToolOutputTruncations int            `json:"tool_output_truncations,omitempty"`
	ToolOutputRefs        int            `json:"tool_output_refs,omitempty"`
	EvaluatorTimeMS       int64          `json:"evaluator_time_ms,omitempty"`
	EvaluatorCommand      []string       `json:"evaluator_command,omitempty"`
	EvaluatorOutput       string         `json:"evaluator_output,omitempty"`
	Error                 string         `json:"error,omitempty"`
}

type Summary struct {
	Total                 int     `json:"total"`
	Passed                int     `json:"passed"`
	Failed                int     `json:"failed"`
	Timeouts              int     `json:"timeouts"`
	Crashes               int     `json:"crashes"`
	PassRate              float64 `json:"pass_rate"`
	WallTimeMS            int64   `json:"wall_time_ms"`
	ModelCalls            int     `json:"model_calls"`
	ToolCalls             int     `json:"tool_calls"`
	InputTokens           int64   `json:"input_tokens,omitempty"`
	OutputTokens          int64   `json:"output_tokens,omitempty"`
	CacheHitTokens        int64   `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens       int64   `json:"cache_miss_tokens,omitempty"`
	ContextCompactions    int     `json:"context_compactions,omitempty"`
	ToolOutputTruncations int     `json:"tool_output_truncations,omitempty"`
	ToolOutputRefs        int     `json:"tool_output_refs,omitempty"`
	ManifestJSON          string  `json:"manifest_json,omitempty"`
	ResultsJSONL          string  `json:"results_jsonl"`
	EventsJSONL           string  `json:"events_jsonl"`
	PayloadsDir           string  `json:"payloads_dir,omitempty"`
	ReplayVerified        bool    `json:"replay_verified,omitempty"`
}

func Run(ctx context.Context, cfg config.Config, rc RunConfig) (Summary, error) {
	tasks, err := LoadTasks(rc.TasksPath)
	if err != nil {
		return Summary{}, err
	}
	if rc.Limit > 0 && rc.Limit < len(tasks) {
		tasks = tasks[:rc.Limit]
	}
	if len(tasks) == 0 {
		return Summary{}, fmt.Errorf("no tasks")
	}
	if err := ensurePrivateDir(rc.OutDir); err != nil {
		return Summary{}, err
	}
	cfg = applyRunConfig(cfg, rc)
	runID := time.Now().UTC().Format("20060102T150405Z")
	manifestPath := filepath.Join(rc.OutDir, runID+"-manifest.json")
	resultsPath := filepath.Join(rc.OutDir, runID+"-results.jsonl")
	eventsPath := filepath.Join(rc.OutDir, runID+"-events.jsonl")
	payloadsDir := filepath.Join(rc.OutDir, runID+"-payloads")
	resultsFile, err := createPrivateFile(resultsPath)
	if err != nil {
		return Summary{}, err
	}
	defer resultsFile.Close()
	eventsFile, err := createPrivateFile(eventsPath)
	if err != nil {
		return Summary{}, err
	}
	defer eventsFile.Close()
	resultEnc := json.NewEncoder(resultsFile)
	if err := ensurePrivateDir(payloadsDir); err != nil {
		return Summary{}, err
	}
	eventWriter := trace.NewEventWriter(runID, eventsFile,
		trace.WithSanitizer(redactForPersistence),
		trace.WithPayloadDir(payloadsDir, shouldWritePayloadRef),
	)

	summary := Summary{
		Total:        len(tasks),
		ManifestJSON: manifestPath,
		ResultsJSONL: resultsPath,
		EventsJSONL:  eventsPath,
		PayloadsDir:  payloadsDir,
	}
	startAll := time.Now()
	for _, task := range tasks {
		result := runTask(ctx, cfg, rc, runID, task, eventWriter)
		if err := encodeRedactedJSON(resultEnc, result); err != nil {
			return summary, err
		}
		summary.ModelCalls += result.ModelCalls
		summary.ToolCalls += result.ToolCalls
		summary.InputTokens += result.InputTokens
		summary.OutputTokens += result.OutputTokens
		summary.CacheHitTokens += result.CacheHitTokens
		summary.CacheMissTokens += result.CacheMissTokens
		summary.ContextCompactions += result.ContextCompactions
		summary.ToolOutputTruncations += result.ToolOutputTruncations
		summary.ToolOutputRefs += result.ToolOutputRefs
		switch result.Outcome {
		case "pass":
			summary.Passed++
		case "timeout":
			summary.Timeouts++
		case "crash":
			summary.Crashes++
		default:
			summary.Failed++
		}
	}
	summary.WallTimeMS = time.Since(startAll).Milliseconds()
	if summary.Total > 0 {
		summary.PassRate = float64(summary.Passed) / float64(summary.Total)
	}
	if replay, err := trace.ReplayEvents(eventsPath); err == nil && replay.Records > 0 {
		summary.ReplayVerified = true
	} else if err != nil {
		return summary, err
	}
	if err := trace.WriteManifest(manifestPath, trace.Manifest{
		RunID:        runID,
		CreatedAt:    startAll.UTC(),
		StartedAtMS:  startAll.UTC().UnixMilli(),
		Harness:      "fast-agent-harness-go",
		TasksPath:    rc.TasksPath,
		TaskCount:    len(tasks),
		ResultsJSONL: resultsPath,
		EventsJSONL:  eventsPath,
		PayloadsDir:  payloadsDir,
	}); err != nil {
		return summary, err
	}
	return summary, nil
}

func applyRunConfig(cfg config.Config, rc RunConfig) config.Config {
	if rc.Mock {
		cfg.Provider = "mock"
		cfg.Model = "mock"
	}
	if rc.Model != "" {
		cfg.Model = rc.Model
	}
	if rc.ContextCompactTokens > 0 {
		cfg.ContextCompactTokens = rc.ContextCompactTokens
	}
	if rc.ContextCompactKeep > 0 {
		cfg.ContextCompactKeep = rc.ContextCompactKeep
	}
	if rc.ContextCompactMaxChars > 0 {
		cfg.ContextCompactMaxChars = rc.ContextCompactMaxChars
	}
	cfg.ApplyModelProviderDefaults()
	return cfg
}

func LoadTasks(path string) ([]Task, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var tasks []Task
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var task Task
		if err := json.Unmarshal([]byte(line), &task); err != nil {
			return nil, err
		}
		if task.ID == "" {
			return nil, fmt.Errorf("task missing id")
		}
		if task.Prompt == "" {
			return nil, fmt.Errorf("task %s missing prompt", task.ID)
		}
		if task.Suite == "" {
			task.Suite = "custom"
		}
		tasks = append(tasks, task)
	}
	return tasks, scanner.Err()
}

func runTask(parent context.Context, cfg config.Config, rc RunConfig, runID string, task Task, eventWriter *trace.EventWriter) Result {
	timeout := rc.Timeout
	if task.TimeoutSeconds > 0 {
		timeout = time.Duration(task.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	workspace, workspaceErr := prepareWorkspace(rc.OutDir, runID, task)
	taskCfg := cfg
	taskCfg.WorkspaceRoots = []string{workspace}
	applyTaskConfig(&taskCfg, task)
	result := Result{
		RunID:           runID,
		TaskID:          task.ID,
		TaskSuite:       task.Suite,
		Model:           taskCfg.Model,
		Harness:         "fast-agent-harness-go",
		PromptHash:      promptHash(task.Prompt),
		Workspace:       workspace,
		Tags:            task.Tags,
		Outcome:         "fail",
		ToolCallsByName: map[string]int{},
	}
	start := time.Now()
	if workspaceErr != nil {
		result.Outcome = "crash"
		result.Error = "workspace failed: " + workspaceErr.Error()
		result.WallTimeMS = time.Since(start).Milliseconds()
		return result
	}
	if err := runCommands(ctx, workspace, task.Setup); err != nil {
		result.Outcome = "crash"
		result.Error = "setup failed: " + err.Error()
		result.WallTimeMS = time.Since(start).Milliseconds()
		return result
	}
	prov, err := taskProvider(taskCfg, rc, task)
	if err != nil {
		result.Outcome = "crash"
		result.Error = err.Error()
		result.WallTimeMS = time.Since(start).Milliseconds()
		return result
	}
	registry, err := tools.NewRegistryWithMCP(ctx, taskCfg)
	if err != nil {
		result.Outcome = "crash"
		result.Error = err.Error()
		result.WallTimeMS = time.Since(start).Milliseconds()
		return result
	}
	defer registry.Close()
	a := agent.New(taskCfg, prov, registry)
	var eventWriteErr error
	err = a.Run(ctx, task.Prompt, func(event protocol.Event) {
		observe(&result, event)
		if eventWriteErr != nil {
			return
		}
		_, eventWriteErr = eventWriter.Record(task.ID, event)
	})
	if eventWriteErr != nil {
		result.Outcome = "crash"
		result.Error = "event trace failed: " + eventWriteErr.Error()
		result.WallTimeMS = time.Since(start).Milliseconds()
		return result
	}
	if err != nil {
		if ctx.Err() != nil {
			result.Outcome = "timeout"
			result.Error = ctx.Err().Error()
		} else {
			result.Outcome = "crash"
			result.Error = err.Error()
		}
		result.WallTimeMS = time.Since(start).Milliseconds()
		return result
	}
	if len(task.Evaluator) > 0 {
		evalStart := time.Now()
		output, err := runCommand(ctx, workspace, task.Evaluator)
		result.EvaluatorTimeMS = time.Since(evalStart).Milliseconds()
		result.EvaluatorCommand = task.Evaluator
		result.EvaluatorOutput = truncate(output, 16*1024)
		if err != nil {
			result.Outcome = "fail"
			result.Error = "evaluator failed: " + err.Error()
		} else {
			result.Outcome = "pass"
		}
	} else {
		if result.ToolErrors > 0 {
			result.Outcome = "fail"
			result.Error = fmt.Sprintf("%d tool error(s)", result.ToolErrors)
		} else {
			result.Outcome = "pass"
		}
	}
	result.WallTimeMS = time.Since(start).Milliseconds()
	return result
}

func shouldWritePayloadRef(event protocol.Event) bool {
	switch event.Type {
	case protocol.EventToolCallRequested, protocol.EventToolCallFinished, protocol.EventContextCompacted, protocol.EventRunFailed:
		return true
	default:
		return false
	}
}

func observe(result *Result, event protocol.Event) {
	switch event.Type {
	case protocol.EventContextCompacted:
		result.ContextCompactions++
	case protocol.EventModelCallStarted:
		result.ModelCalls++
	case protocol.EventToolCallStarted:
		result.ToolCalls++
		name, _ := event.Data.(string)
		if name != "" {
			result.ToolCallsByName[name]++
		}
	case protocol.EventToolCallFinished:
		toolResult, ok := decodeToolResult(event.Data)
		if ok && toolResult.Truncated {
			result.ToolOutputTruncations++
		}
		if ok && toolResult.OutputRef != "" {
			result.ToolOutputRefs++
		}
		if toolResultIsError(event.Data) {
			result.ToolErrors++
		}
	case protocol.EventProviderUsageUpdate:
		bytes, _ := json.Marshal(event.Data)
		var usage struct {
			InputTokens     int64 `json:"input_tokens"`
			OutputTokens    int64 `json:"output_tokens"`
			CacheHitTokens  int64 `json:"cache_hit_tokens"`
			CacheMissTokens int64 `json:"cache_miss_tokens"`
		}
		if err := json.Unmarshal(bytes, &usage); err == nil {
			result.InputTokens += usage.InputTokens
			result.OutputTokens += usage.OutputTokens
			result.CacheHitTokens += usage.CacheHitTokens
			result.CacheMissTokens += usage.CacheMissTokens
		}
	}
}

func applyTaskConfig(cfg *config.Config, task Task) {
	if task.ContextCompactTokens > 0 {
		cfg.ContextCompactTokens = task.ContextCompactTokens
	}
	if task.ContextCompactKeep > 0 {
		cfg.ContextCompactKeep = task.ContextCompactKeep
	}
	if task.ContextCompactMaxChars > 0 {
		cfg.ContextCompactMaxChars = task.ContextCompactMaxChars
	}
}

func taskProvider(cfg config.Config, rc RunConfig, task Task) (provider.Provider, error) {
	rounds := task.ScriptedToolRounds
	if rounds <= 0 {
		rounds = rc.ScriptedToolRounds
	}
	if rc.Mock && rounds > 0 {
		return newScriptedLoopProvider(rounds, task.ScriptedToolName, task.ScriptedToolArgs), nil
	}
	return provider.New(cfg)
}

func toolResultIsError(value any) bool {
	if text, ok := value.(string); ok {
		return strings.HasPrefix(text, "tool error:")
	}
	result, ok := decodeToolResult(value)
	return ok && result.IsError
}

func decodeToolResult(value any) (protocol.ToolResult, bool) {
	bytes, _ := json.Marshal(value)
	var result protocol.ToolResult
	if err := json.Unmarshal(bytes, &result); err != nil {
		return protocol.ToolResult{}, false
	}
	return result, result.Name != "" || result.CallID != "" || result.Content != ""
}

type scriptedLoopProvider struct {
	rounds   int
	toolName string
	args     string
	mu       sync.Mutex
	calls    int
}

func newScriptedLoopProvider(rounds int, toolName, args string) *scriptedLoopProvider {
	if rounds < 0 {
		rounds = 0
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "time_now"
	}
	args = strings.TrimSpace(args)
	if args == "" {
		args = `{}`
	}
	return &scriptedLoopProvider{rounds: rounds, toolName: toolName, args: args}
}

func (p *scriptedLoopProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event, 4)
	errs := make(chan error, 1)
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	go func() {
		defer close(events)
		defer close(errs)
		usage := provider.Event{Kind: provider.EventUsage, Usage: provider.Usage{
			InputTokens:     int64(100 + call*25),
			OutputTokens:    4,
			CacheHitTokens:  int64(max(0, call-1) * 20),
			CacheMissTokens: int64(100 + call*5),
		}}
		if err := sendProviderEvent(ctx, events, usage); err != nil {
			errs <- err
			return
		}
		if call <= p.rounds {
			if err := sendProviderEvent(ctx, events, provider.Event{
				Kind:      provider.EventToolCallDelta,
				ToolIndex: 0,
				ToolID:    fmt.Sprintf("scripted_%03d", call),
				ToolName:  p.toolName,
				ArgsDelta: p.args,
			}); err != nil {
				errs <- err
				return
			}
			_ = sendProviderEvent(ctx, events, provider.Event{Kind: provider.EventDone})
			return
		}
		if err := sendProviderEvent(ctx, events, provider.Event{
			Kind: provider.EventContent,
			Text: fmt.Sprintf("scripted loop complete after %d tool rounds", p.rounds),
		}); err != nil {
			errs <- err
			return
		}
		_ = sendProviderEvent(ctx, events, provider.Event{Kind: provider.EventDone})
	}()
	return events, errs
}

func sendProviderEvent(ctx context.Context, events chan<- provider.Event, event provider.Event) error {
	select {
	case events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func prepareWorkspace(outDir, runID string, task Task) (string, error) {
	if task.WorkspaceTemplate != "" {
		src, err := filepath.Abs(task.WorkspaceTemplate)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(outDir, runID+"-workspaces", safeTaskOutputName(task.ID))
		if err := os.RemoveAll(dst); err != nil {
			return "", err
		}
		if err := copyDir(src, dst); err != nil {
			return "", err
		}
		return dst, nil
	}
	workspace := task.Workspace
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	return filepath.Abs(workspace)
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace_template is not a directory: %s", src)
	}
	if err := ensurePrivateDir(dst); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeType != 0 && !entryInfo.IsDir() {
			return fmt.Errorf("refusing special file in workspace template: %s", from)
		}
		if entryInfo.IsDir() {
			if err := copyDir(from, to); err != nil {
				return err
			}
			continue
		}
		bytes, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := writePrivateFile(to, bytes, privateRegularFileMode(entryInfo.Mode().Perm())); err != nil {
			return err
		}
	}
	return nil
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func createPrivateFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func writePrivateFile(path string, bytes []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, bytes, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func privateRegularFileMode(mode os.FileMode) os.FileMode {
	private := os.FileMode(0o600)
	if mode&0o111 != 0 {
		private |= 0o100
	}
	return private
}

func safeTaskOutputName(id string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(id) {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "._-")
	if name == "" {
		name = "task"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	if name != strings.Trim(strings.TrimSpace(id), "._-") {
		sum := sha256.Sum256([]byte(id))
		name += "-" + hex.EncodeToString(sum[:4])
	}
	return name
}

func encodeRedactedJSON(enc *json.Encoder, value any) error {
	return enc.Encode(redactForPersistence(value))
}

const redactedValue = "[REDACTED]"

func redactForPersistence(value any) any {
	bytes, err := json.Marshal(value)
	if err != nil {
		if text, ok := value.(string); ok {
			return redactSensitiveText(text)
		}
		return value
	}
	var generic any
	if err := json.Unmarshal(bytes, &generic); err != nil {
		return value
	}
	return redactJSONValue(generic, "")
}

func redactJSONValue(value any, key string) any {
	if sensitivePersistenceKey(key) {
		if value == nil {
			return nil
		}
		return redactedValue
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			out[childKey] = redactJSONValue(childValue, childKey)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, childValue := range typed {
			out[i] = redactJSONValue(childValue, "")
		}
		return out
	case string:
		return redactSensitiveText(typed)
	default:
		return value
	}
}

func sensitivePersistenceKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(normalized)
	if normalized == "" {
		return false
	}
	switch {
	case strings.Contains(normalized, "apikey"),
		strings.Contains(normalized, "privatekey"),
		strings.Contains(normalized, "password"),
		strings.Contains(normalized, "secret"),
		strings.Contains(normalized, "credential"),
		strings.Contains(normalized, "authorization"),
		strings.Contains(normalized, "bearertoken"),
		strings.Contains(normalized, "accesstoken"),
		strings.Contains(normalized, "refreshtoken"),
		strings.HasSuffix(normalized, "token") && !strings.HasSuffix(normalized, "tokens"):
		return true
	default:
		return false
	}
}

var (
	bearerTokenRE         = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/\-=]+`)
	sensitiveAssignmentRE = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:key|token|secret|password|credential|authorization)[A-Za-z0-9_.-]*)\s*([:=])\s*("[^"]*"|'[^']*'|[^\s,;]+)`)
	sensitiveJSONFieldRE  = regexp.MustCompile(`(?i)"([^"]*(?:key|token|secret|password|credential|authorization)[^"]*)"\s*:\s*("[^"]*"|[^\s,}\]]+)`)
)

func redactSensitiveText(text string) string {
	text = bearerTokenRE.ReplaceAllString(text, "Bearer "+redactedValue)
	text = sensitiveAssignmentRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := sensitiveAssignmentRE.FindStringSubmatchIndex(match)
		if len(parts) < 8 {
			return match
		}
		key := match[parts[2]:parts[3]]
		if !sensitivePersistenceKey(key) {
			return match
		}
		return match[:parts[6]] + redactedValue
	})
	text = sensitiveJSONFieldRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := sensitiveJSONFieldRE.FindStringSubmatchIndex(match)
		if len(parts) < 6 {
			return match
		}
		key := match[parts[2]:parts[3]]
		if !sensitivePersistenceKey(key) {
			return match
		}
		return match[:parts[4]] + `"` + redactedValue + `"`
	})
	return text
}

func runCommands(ctx context.Context, cwd string, commands [][]string) error {
	for _, command := range commands {
		if _, err := runCommand(ctx, cwd, command); err != nil {
			return err
		}
	}
	return nil
}

func runCommand(ctx context.Context, cwd string, argv []string) (string, error) {
	if len(argv) == 0 || argv[0] == "" {
		return "", fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	if err != nil {
		return string(output), fmt.Errorf("%w: %s", err, truncate(string(output), 2048))
	}
	return string(output), nil
}

func promptHash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:8])
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("...[truncated %d bytes]", len(s)-n)
}
