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
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Task struct {
	ID                string     `json:"id"`
	Suite             string     `json:"suite"`
	Prompt            string     `json:"prompt"`
	Workspace         string     `json:"workspace,omitempty"`
	WorkspaceTemplate string     `json:"workspace_template,omitempty"`
	Setup             [][]string `json:"setup,omitempty"`
	Evaluator         []string   `json:"evaluator,omitempty"`
	TimeoutSeconds    int        `json:"timeout_seconds,omitempty"`
	Tags              []string   `json:"tags,omitempty"`
}

type RunConfig struct {
	TasksPath string
	OutDir    string
	Limit     int
	Mock      bool
	Model     string
	Timeout   time.Duration
}

type Result struct {
	RunID            string         `json:"run_id"`
	TaskID           string         `json:"task_id"`
	TaskSuite        string         `json:"task_suite"`
	Model            string         `json:"model"`
	Harness          string         `json:"harness"`
	PromptHash       string         `json:"prompt_hash"`
	Workspace        string         `json:"workspace,omitempty"`
	Tags             []string       `json:"tags,omitempty"`
	Outcome          string         `json:"outcome"`
	WallTimeMS       int64          `json:"wall_time_ms"`
	ModelCalls       int            `json:"model_calls"`
	ToolCalls        int            `json:"tool_calls"`
	ToolCallsByName  map[string]int `json:"tool_calls_by_name,omitempty"`
	ToolErrors       int            `json:"tool_errors"`
	InputTokens      int64          `json:"input_tokens,omitempty"`
	OutputTokens     int64          `json:"output_tokens,omitempty"`
	EvaluatorTimeMS  int64          `json:"evaluator_time_ms,omitempty"`
	EvaluatorCommand []string       `json:"evaluator_command,omitempty"`
	EvaluatorOutput  string         `json:"evaluator_output,omitempty"`
	Error            string         `json:"error,omitempty"`
}

type Summary struct {
	Total        int     `json:"total"`
	Passed       int     `json:"passed"`
	Failed       int     `json:"failed"`
	Timeouts     int     `json:"timeouts"`
	Crashes      int     `json:"crashes"`
	PassRate     float64 `json:"pass_rate"`
	WallTimeMS   int64   `json:"wall_time_ms"`
	ModelCalls   int     `json:"model_calls"`
	ToolCalls    int     `json:"tool_calls"`
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	ResultsJSONL string  `json:"results_jsonl"`
	EventsJSONL  string  `json:"events_jsonl"`
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
	if err := os.MkdirAll(rc.OutDir, 0o755); err != nil {
		return Summary{}, err
	}
	if rc.Mock {
		cfg.Provider = "mock"
		cfg.Model = "mock"
	}
	if rc.Model != "" {
		cfg.Model = rc.Model
	}
	runID := time.Now().UTC().Format("20060102T150405Z")
	resultsPath := filepath.Join(rc.OutDir, runID+"-results.jsonl")
	eventsPath := filepath.Join(rc.OutDir, runID+"-events.jsonl")
	resultsFile, err := os.Create(resultsPath)
	if err != nil {
		return Summary{}, err
	}
	defer resultsFile.Close()
	eventsFile, err := os.Create(eventsPath)
	if err != nil {
		return Summary{}, err
	}
	defer eventsFile.Close()
	resultEnc := json.NewEncoder(resultsFile)
	eventEnc := json.NewEncoder(eventsFile)

	summary := Summary{Total: len(tasks), ResultsJSONL: resultsPath, EventsJSONL: eventsPath}
	startAll := time.Now()
	for _, task := range tasks {
		result := runTask(ctx, cfg, rc, runID, task, eventEnc)
		if err := resultEnc.Encode(result); err != nil {
			return summary, err
		}
		summary.ModelCalls += result.ModelCalls
		summary.ToolCalls += result.ToolCalls
		summary.InputTokens += result.InputTokens
		summary.OutputTokens += result.OutputTokens
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
	return summary, nil
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

func runTask(parent context.Context, cfg config.Config, rc RunConfig, runID string, task Task, eventEnc *json.Encoder) Result {
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
	prov, err := provider.New(taskCfg)
	if err != nil {
		result.Outcome = "crash"
		result.Error = err.Error()
		result.WallTimeMS = time.Since(start).Milliseconds()
		return result
	}
	registry := tools.NewRegistry(taskCfg)
	a := agent.New(taskCfg, prov, registry)
	err = a.Run(ctx, task.Prompt, func(event protocol.Event) {
		observe(&result, event)
		_ = eventEnc.Encode(map[string]any{
			"run_id":  runID,
			"task_id": task.ID,
			"ts":      time.Now().UTC().Format(time.RFC3339Nano),
			"event":   event,
		})
	})
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
		result.Outcome = "pass"
	}
	result.WallTimeMS = time.Since(start).Milliseconds()
	return result
}

func observe(result *Result, event protocol.Event) {
	switch event.Type {
	case protocol.EventModelCallStarted:
		result.ModelCalls++
	case protocol.EventToolCallStarted:
		result.ToolCalls++
		name, _ := event.Data.(string)
		if name != "" {
			result.ToolCallsByName[name]++
		}
	case protocol.EventToolCallFinished:
		if text, ok := event.Data.(string); ok && strings.HasPrefix(text, "tool error:") {
			result.ToolErrors++
		}
	case protocol.EventProviderUsageUpdate:
		bytes, _ := json.Marshal(event.Data)
		var usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		}
		if err := json.Unmarshal(bytes, &usage); err == nil {
			result.InputTokens += usage.InputTokens
			result.OutputTokens += usage.OutputTokens
		}
	}
}

func prepareWorkspace(outDir, runID string, task Task) (string, error) {
	if task.WorkspaceTemplate != "" {
		src, err := filepath.Abs(task.WorkspaceTemplate)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(outDir, runID+"-workspaces", task.ID)
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
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
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
		if err := os.WriteFile(to, bytes, entryInfo.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
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
