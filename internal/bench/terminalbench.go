package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	TerminalBenchSuite       = "terminal-bench"
	terminalBenchMetaFile    = ".billyharness-task.json"
	terminalBenchDefaultFrom = "ghcr.io/laude-institute/t-bench/ubuntu-24-04:20250624"
)

type TerminalBenchExportOptions struct {
	TasksPath             string
	OutDir                string
	Force                 bool
	AuthorName            string
	AuthorEmail           string
	Difficulty            string
	Category              string
	MaxTestTimeoutSeconds int
}

type TerminalBenchExportSummary struct {
	DatasetPath string   `json:"dataset_path"`
	Tasks       int      `json:"tasks"`
	TaskIDs     []string `json:"task_ids"`
	RunCommand  []string `json:"run_command"`
}

type TerminalBenchImportOptions struct {
	DatasetDir string
	Suite      string
}

type terminalBenchMetadata struct {
	SchemaVersion int       `json:"schema_version"`
	Source        string    `json:"source"`
	ExportedAt    time.Time `json:"exported_at"`
	TaskDirID     string    `json:"task_dir_id"`
	Task          Task      `json:"task"`
}

type terminalBenchTaskConfig struct {
	Instruction        string
	Tags               []string
	MaxAgentTimeoutSec float64
}

func ExportTerminalBenchDataset(opts TerminalBenchExportOptions) (TerminalBenchExportSummary, error) {
	if strings.TrimSpace(opts.TasksPath) == "" {
		return TerminalBenchExportSummary{}, fmt.Errorf("tasks path required")
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		return TerminalBenchExportSummary{}, fmt.Errorf("output directory required")
	}
	opts = withTerminalBenchExportDefaults(opts)
	tasks, err := LoadTasks(opts.TasksPath)
	if err != nil {
		return TerminalBenchExportSummary{}, err
	}
	if len(tasks) == 0 {
		return TerminalBenchExportSummary{}, fmt.Errorf("no tasks")
	}
	if err := prepareTerminalBenchOutDir(opts.OutDir, opts.Force); err != nil {
		return TerminalBenchExportSummary{}, err
	}
	if err := validateTerminalBenchDifficulty(opts.Difficulty); err != nil {
		return TerminalBenchExportSummary{}, err
	}

	seen := map[string]bool{}
	summary := TerminalBenchExportSummary{
		DatasetPath: opts.OutDir,
		TaskIDs:     make([]string, 0, len(tasks)),
		RunCommand: []string{
			"tb", "run",
			"--dataset-path", opts.OutDir,
			"--agent", "nop",
			"--output-path", "tb-runs/billyharness",
			"--no-upload-results",
			"--no-livestream",
		},
	}
	for _, task := range tasks {
		taskDirID := safeTaskOutputName(task.ID)
		if seen[taskDirID] {
			return summary, fmt.Errorf("duplicate Terminal-Bench task directory %q from task %q", taskDirID, task.ID)
		}
		seen[taskDirID] = true
		taskDir := filepath.Join(opts.OutDir, taskDirID)
		if err := exportTerminalBenchTask(opts, task, taskDirID, taskDir); err != nil {
			return summary, err
		}
		summary.TaskIDs = append(summary.TaskIDs, taskDirID)
	}
	summary.Tasks = len(summary.TaskIDs)
	return summary, nil
}

func ImportTerminalBenchDataset(opts TerminalBenchImportOptions) ([]Task, error) {
	if strings.TrimSpace(opts.DatasetDir) == "" {
		return nil, fmt.Errorf("dataset directory required")
	}
	suite := strings.TrimSpace(opts.Suite)
	if suite == "" {
		suite = TerminalBenchSuite
	}
	entries, err := os.ReadDir(opts.DatasetDir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var tasks []Task
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskDir := filepath.Join(opts.DatasetDir, entry.Name())
		taskYAMLPath := filepath.Join(taskDir, "task.yaml")
		if _, err := os.Stat(taskYAMLPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		tbTask, err := parseTerminalBenchTaskYAML(taskYAMLPath)
		if err != nil {
			return nil, err
		}
		task, err := importTerminalBenchTask(taskDir, entry.Name(), suite, tbTask)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no Terminal-Bench tasks found in %s", opts.DatasetDir)
	}
	return tasks, nil
}

func EncodeTasksJSONL(w io.Writer, tasks []Task) error {
	enc := json.NewEncoder(w)
	for _, task := range tasks {
		if err := enc.Encode(task); err != nil {
			return err
		}
	}
	return nil
}

func WriteTasksJSONL(path string, tasks []Task) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	return EncodeTasksJSONL(file, tasks)
}

func withTerminalBenchExportDefaults(opts TerminalBenchExportOptions) TerminalBenchExportOptions {
	if strings.TrimSpace(opts.AuthorName) == "" {
		opts.AuthorName = "billyharness"
	}
	if strings.TrimSpace(opts.AuthorEmail) == "" {
		opts.AuthorEmail = "unknown"
	}
	if strings.TrimSpace(opts.Difficulty) == "" {
		opts.Difficulty = "unknown"
	}
	if strings.TrimSpace(opts.Category) == "" {
		opts.Category = "software_engineering"
	}
	if opts.MaxTestTimeoutSeconds <= 0 {
		opts.MaxTestTimeoutSeconds = 60
	}
	return opts
}

func validateTerminalBenchDifficulty(difficulty string) error {
	switch strings.TrimSpace(strings.ToLower(difficulty)) {
	case "easy", "medium", "hard", "unknown":
		return nil
	default:
		return fmt.Errorf("unsupported Terminal-Bench difficulty %q", difficulty)
	}
}

func prepareTerminalBenchOutDir(path string, force bool) error {
	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("refusing to use unsafe Terminal-Bench output directory %q", path)
	}
	if force {
		if err := os.RemoveAll(clean); err != nil {
			return err
		}
		return os.MkdirAll(clean, 0o755)
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(clean)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("Terminal-Bench output directory is not empty: %s (use -force to replace it)", clean)
	}
	return nil
}

func exportTerminalBenchTask(opts TerminalBenchExportOptions, task Task, taskDirID, taskDir string) error {
	if _, err := os.Stat(taskDir); err == nil {
		return fmt.Errorf("Terminal-Bench task directory already exists after preparing output: %s", taskDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	testsDir := filepath.Join(taskDir, "tests")
	workspaceDir := filepath.Join(taskDir, "workspace")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return err
	}
	if err := exportWorkspace(task, workspaceDir); err != nil {
		return fmt.Errorf("export workspace for task %s: %w", task.ID, err)
	}
	files := map[string]struct {
		body string
		mode os.FileMode
	}{
		filepath.Join(taskDir, "task.yaml"): {
			body: terminalBenchTaskYAML(opts, task),
			mode: 0o644,
		},
		filepath.Join(taskDir, "Dockerfile"): {
			body: terminalBenchDockerfile(),
			mode: 0o644,
		},
		filepath.Join(taskDir, "docker-compose.yaml"): {
			body: terminalBenchDockerComposeYAML(),
			mode: 0o644,
		},
		filepath.Join(taskDir, "run-tests.sh"): {
			body: terminalBenchRunTestsScript(),
			mode: 0o755,
		},
		filepath.Join(testsDir, "billyharness_evaluator.py"): {
			body: terminalBenchEvaluatorPython(),
			mode: 0o755,
		},
		filepath.Join(testsDir, "test_outputs.py"): {
			body: terminalBenchPytestStub(),
			mode: 0o644,
		},
		filepath.Join(taskDir, "solution.sh"): {
			body: terminalBenchSolutionStub(),
			mode: 0o755,
		},
	}
	for path, file := range files {
		if err := os.WriteFile(path, []byte(file.body), file.mode); err != nil {
			return err
		}
		if err := os.Chmod(path, file.mode); err != nil {
			return err
		}
	}
	evaluatorJSON, err := json.MarshalIndent(map[string]any{
		"argv": task.Evaluator,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(testsDir, "billyharness-evaluator.json"), append(evaluatorJSON, '\n'), 0o644); err != nil {
		return err
	}
	meta, err := json.MarshalIndent(terminalBenchMetadata{
		SchemaVersion: 1,
		Source:        "billyharness",
		ExportedAt:    time.Now().UTC(),
		TaskDirID:     taskDirID,
		Task:          task,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(taskDir, terminalBenchMetaFile), append(meta, '\n'), 0o644)
}

func exportWorkspace(task Task, dst string) error {
	src := strings.TrimSpace(task.WorkspaceTemplate)
	if src == "" {
		src = strings.TrimSpace(task.Workspace)
	}
	if src == "" {
		return nil
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	return copyPublicDir(abs, dst)
}

func copyPublicDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace source is not a directory: %s", src)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
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
			return fmt.Errorf("refusing special file in workspace: %s", from)
		}
		if entryInfo.IsDir() {
			if err := copyPublicDir(from, to); err != nil {
				return err
			}
			continue
		}
		bytes, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if entryInfo.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(to, bytes, mode); err != nil {
			return err
		}
		if err := os.Chmod(to, mode); err != nil {
			return err
		}
	}
	return nil
}

func terminalBenchTaskYAML(opts TerminalBenchExportOptions, task Task) string {
	var b strings.Builder
	writeYAMLBlock(&b, "instruction", task.Prompt)
	writeYAMLString(&b, "author_name", opts.AuthorName)
	writeYAMLString(&b, "author_email", opts.AuthorEmail)
	writeYAMLString(&b, "difficulty", strings.ToLower(opts.Difficulty))
	writeYAMLString(&b, "category", opts.Category)
	writeYAMLStringList(&b, "tags", terminalBenchTags(task))
	writeYAMLString(&b, "parser_name", "pytest")
	b.WriteString("max_agent_timeout_sec: ")
	b.WriteString(strconv.FormatFloat(float64(terminalBenchAgentTimeout(task)), 'f', -1, 64))
	b.WriteByte('\n')
	b.WriteString("max_test_timeout_sec: ")
	b.WriteString(strconv.Itoa(opts.MaxTestTimeoutSeconds))
	b.WriteByte('\n')
	b.WriteString("run_tests_in_same_shell: false\n")
	b.WriteString("disable_asciinema: false\n")
	return b.String()
}

func terminalBenchTags(task Task) []string {
	tags := []string{"billyharness"}
	if task.Suite != "" {
		tags = append(tags, task.Suite)
	}
	tags = append(tags, task.Tags...)
	return uniqueStrings(tags)
}

func terminalBenchAgentTimeout(task Task) int {
	if task.TimeoutSeconds > 0 {
		return task.TimeoutSeconds
	}
	return 360
}

func terminalBenchDockerfile() string {
	return fmt.Sprintf(`FROM %s

WORKDIR /app
COPY workspace/ /app/
`, terminalBenchDefaultFrom)
}

func terminalBenchDockerComposeYAML() string {
	return `services:
  client:
    build:
      dockerfile: Dockerfile
    image: ${T_BENCH_TASK_DOCKER_CLIENT_IMAGE_NAME}
    container_name: ${T_BENCH_TASK_DOCKER_CLIENT_CONTAINER_NAME}
    command: [ "sh", "-c", "sleep infinity" ]
    environment:
      - TEST_DIR=${T_BENCH_TEST_DIR}
    volumes:
      - ${T_BENCH_TASK_LOGS_PATH}:${T_BENCH_CONTAINER_LOGS_PATH}
      - ${T_BENCH_TASK_AGENT_LOGS_PATH}:${T_BENCH_CONTAINER_AGENT_LOGS_PATH}
`
}

func terminalBenchRunTestsScript() string {
	return `#!/bin/bash
set -u

script_dir="$(cd "$(dirname "$0")" && pwd)"
test_dir="${TEST_DIR:-$script_dir/tests}"
app_dir="${APP_DIR:-/app}"

export TEST_DIR="$test_dir"
export APP_DIR="$app_dir"

python3 "$test_dir/billyharness_evaluator.py"
status=$?

echo "============================= short test summary info ============================="
if [ "$status" -eq 0 ]; then
  echo "PASSED tests/test_outputs.py::test_billyharness_evaluator"
else
  echo "FAILED tests/test_outputs.py::test_billyharness_evaluator - evaluator exited with status $status"
fi

exit "$status"
`
}

func terminalBenchEvaluatorPython() string {
	return `#!/usr/bin/env python3
import json
import os
import subprocess
import sys


def main() -> int:
    test_dir = os.environ.get("TEST_DIR") or os.path.dirname(__file__)
    app_dir = os.environ.get("APP_DIR") or os.getcwd()
    config_path = os.path.join(test_dir, "billyharness-evaluator.json")
    with open(config_path, "r", encoding="utf-8") as f:
        config = json.load(f)

    argv = config.get("argv") or []
    if not argv:
        print("billyharness evaluator: no evaluator command configured")
        return 0

    completed = subprocess.run(
        argv,
        cwd=app_dir,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    if completed.stdout:
        print(completed.stdout, end="")
    return completed.returncode


if __name__ == "__main__":
    sys.exit(main())
`
}

func terminalBenchPytestStub() string {
	return `def test_billyharness_evaluator():
    """The real evaluator is run by run-tests.sh for shell-argv fidelity."""
    assert True
`
}

func terminalBenchSolutionStub() string {
	return `#!/bin/bash
echo "No oracle solution is exported by the billyharness Terminal-Bench adapter." >&2
exit 1
`
}

func importTerminalBenchTask(taskDir, taskDirID, suite string, tbTask terminalBenchTaskConfig) (Task, error) {
	task := Task{
		ID:             taskDirID,
		Suite:          suite,
		Prompt:         tbTask.Instruction,
		Tags:           uniqueStrings(append([]string{TerminalBenchSuite}, tbTask.Tags...)),
		TimeoutSeconds: int(math.Ceil(tbTask.MaxAgentTimeoutSec)),
	}
	if task.TimeoutSeconds <= 0 {
		task.TimeoutSeconds = 360
	}

	metaPath := filepath.Join(taskDir, terminalBenchMetaFile)
	if bytes, err := os.ReadFile(metaPath); err == nil {
		var meta terminalBenchMetadata
		if err := json.Unmarshal(bytes, &meta); err != nil {
			return Task{}, err
		}
		task = meta.Task
		if task.ID == "" {
			task.ID = taskDirID
		}
		if task.Suite == "" {
			task.Suite = suite
		}
		task.Prompt = tbTask.Instruction
		if task.TimeoutSeconds <= 0 {
			task.TimeoutSeconds = int(math.Ceil(tbTask.MaxAgentTimeoutSec))
		}
	} else if !os.IsNotExist(err) {
		return Task{}, err
	}

	workspaceDir := filepath.Join(taskDir, "workspace")
	if info, err := os.Stat(workspaceDir); err == nil && info.IsDir() {
		task.Workspace = ""
		task.WorkspaceTemplate = workspaceDir
	} else if err != nil && !os.IsNotExist(err) {
		return Task{}, err
	}
	if len(task.Evaluator) == 0 {
		runTests := filepath.Join(taskDir, "run-tests.sh")
		if _, err := os.Stat(runTests); err == nil {
			task.Evaluator = terminalBenchRunTestsEvaluator(taskDir)
		} else if err != nil && !os.IsNotExist(err) {
			return Task{}, err
		}
	}
	if task.Prompt == "" {
		return Task{}, fmt.Errorf("Terminal-Bench task %s missing instruction", taskDirID)
	}
	return task, nil
}

func terminalBenchRunTestsEvaluator(taskDir string) []string {
	runTests := filepath.Join(taskDir, "run-tests.sh")
	testsDir := filepath.Join(taskDir, "tests")
	return []string{
		"sh",
		"-c",
		"TEST_DIR=" + shellQuote(testsDir) + " APP_DIR=\"$PWD\" bash " + shellQuote(runTests),
	}
}

func parseTerminalBenchTaskYAML(path string) (terminalBenchTaskConfig, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return terminalBenchTaskConfig{}, err
	}
	lines := strings.Split(strings.ReplaceAll(string(bytes), "\r\n", "\n"), "\n")
	var task terminalBenchTaskConfig
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") || isIndentedYAMLLine(line) {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "instruction":
			if strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
				block, next := readYAMLBlock(lines, i+1)
				task.Instruction = block
				i = next - 1
			} else {
				task.Instruction = parseYAMLScalar(value)
			}
		case "tags":
			tags, next := readYAMLStringList(value, lines, i+1)
			task.Tags = tags
			i = next - 1
		case "max_agent_timeout_sec":
			if value != "" {
				if parsed, err := strconv.ParseFloat(parseYAMLScalar(value), 64); err == nil {
					task.MaxAgentTimeoutSec = parsed
				}
			}
		}
	}
	if strings.TrimSpace(task.Instruction) == "" {
		return terminalBenchTaskConfig{}, fmt.Errorf("task.yaml missing instruction: %s", path)
	}
	return task, nil
}

func writeYAMLBlock(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": |-\n")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimRight(value, "\n")
	if value == "" {
		b.WriteString("  \n")
		return
	}
	for _, line := range strings.Split(value, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func writeYAMLString(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(strconv.Quote(value))
	b.WriteByte('\n')
}

func writeYAMLStringList(b *strings.Builder, key string, values []string) {
	b.WriteString(key)
	b.WriteString(":\n")
	for _, value := range values {
		b.WriteString("  - ")
		b.WriteString(strconv.Quote(value))
		b.WriteByte('\n')
	}
}

func isIndentedYAMLLine(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func readYAMLBlock(lines []string, start int) (string, int) {
	var out []string
	i := start
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) != "" && !isIndentedYAMLLine(line) {
			break
		}
		if strings.HasPrefix(line, "  ") {
			line = strings.TrimPrefix(line, "  ")
		} else if strings.HasPrefix(line, "\t") {
			line = strings.TrimPrefix(line, "\t")
		}
		out = append(out, line)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n"), i
}

func readYAMLStringList(value string, lines []string, start int) ([]string, int) {
	if strings.HasPrefix(value, "[") {
		var tags []string
		if err := json.Unmarshal([]byte(value), &tags); err == nil {
			return uniqueStrings(tags), start
		}
	}
	var tags []string
	i := start
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !isIndentedYAMLLine(line) {
			break
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") {
			tags = append(tags, parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
		}
	}
	return uniqueStrings(tags), i
}

func parseYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	var b bytes.Buffer
	b.WriteByte('\'')
	for _, r := range s {
		if r == '\'' {
			b.WriteString(`'\''`)
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
