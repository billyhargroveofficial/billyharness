package bench

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportTerminalBenchDatasetWritesTaskDirectoryAndEvaluatorBridge(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("write src/answer.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasksPath := filepath.Join(root, "tasks.jsonl")
	taskLine := `{"id":"tb-js-001","suite":"local-code-smoke","workspace_template":` + quote(workspace) + `,"tags":["code"],"timeout_seconds":90,"prompt":"Create src/answer.txt containing ok.","evaluator":["sh","-c","grep -q ok src/answer.txt"]}` + "\n"
	if err := os.WriteFile(tasksPath, []byte(taskLine), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(root, "tb-dataset")
	summary, err := ExportTerminalBenchDataset(TerminalBenchExportOptions{
		TasksPath:             tasksPath,
		OutDir:                outDir,
		MaxTestTimeoutSeconds: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tasks != 1 || len(summary.TaskIDs) != 1 || summary.TaskIDs[0] != "tb-js-001" {
		t.Fatalf("summary = %#v", summary)
	}
	if !containsAll(summary.RunCommand, "--dataset-path", outDir) {
		t.Fatalf("run command missing dataset path: %#v", summary.RunCommand)
	}

	taskDir := filepath.Join(outDir, "tb-js-001")
	taskYAML := readText(t, filepath.Join(taskDir, "task.yaml"))
	for _, want := range []string{
		"instruction: |-",
		"Create src/answer.txt containing ok.",
		`difficulty: "unknown"`,
		"max_agent_timeout_sec: 90",
		"max_test_timeout_sec: 7",
		`  - "billyharness"`,
		`  - "local-code-smoke"`,
	} {
		if !strings.Contains(taskYAML, want) {
			t.Fatalf("task.yaml missing %q:\n%s", want, taskYAML)
		}
	}
	assertFileMode(t, filepath.Join(taskDir, "run-tests.sh"), 0o755)
	if _, err := os.Stat(filepath.Join(taskDir, "workspace", "README.md")); err != nil {
		t.Fatal(err)
	}

	var evaluator struct {
		Argv []string `json:"argv"`
	}
	evaluatorBytes, err := os.ReadFile(filepath.Join(taskDir, "tests", "billyharness-evaluator.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(evaluatorBytes, &evaluator); err != nil {
		t.Fatal(err)
	}
	if strings.Join(evaluator.Argv, "\x00") != "sh\x00-c\x00grep -q ok src/answer.txt" {
		t.Fatalf("evaluator argv = %#v", evaluator.Argv)
	}

	output, err := runTerminalBenchBridge(taskDir, filepath.Join(taskDir, "workspace"))
	if err == nil || !strings.Contains(output, "FAILED tests/test_outputs.py::test_billyharness_evaluator") {
		t.Fatalf("bridge should fail before answer exists, err=%v output=%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "workspace", "src", "answer.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = runTerminalBenchBridge(taskDir, filepath.Join(taskDir, "workspace"))
	if err != nil {
		t.Fatalf("bridge should pass after answer exists: %v\n%s", err, output)
	}
	if !strings.Contains(output, "PASSED tests/test_outputs.py::test_billyharness_evaluator") {
		t.Fatalf("bridge output missing pytest-like pass line: %s", output)
	}
}

func TestImportTerminalBenchDatasetRoundTripsExportedTask(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasksPath := filepath.Join(root, "tasks.jsonl")
	taskLine := `{"id":"roundtrip","suite":"suite-a","workspace_template":` + quote(workspace) + `,"timeout_seconds":123,"prompt":"Do the thing.","evaluator":["true"]}` + "\n"
	if err := os.WriteFile(tasksPath, []byte(taskLine), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "tb-dataset")
	if _, err := ExportTerminalBenchDataset(TerminalBenchExportOptions{TasksPath: tasksPath, OutDir: outDir}); err != nil {
		t.Fatal(err)
	}

	tasks, err := ImportTerminalBenchDataset(TerminalBenchImportOptions{DatasetDir: outDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %#v", tasks)
	}
	got := tasks[0]
	if got.ID != "roundtrip" || got.Suite != "suite-a" || got.Prompt != "Do the thing." || got.TimeoutSeconds != 123 {
		t.Fatalf("imported task = %#v", got)
	}
	if strings.Join(got.Evaluator, "\x00") != "true" {
		t.Fatalf("evaluator = %#v", got.Evaluator)
	}
	if got.WorkspaceTemplate != filepath.Join(outDir, "roundtrip", "workspace") {
		t.Fatalf("workspace template = %q", got.WorkspaceTemplate)
	}

	var buf bytes.Buffer
	if err := EncodeTasksJSONL(&buf, tasks); err != nil {
		t.Fatal(err)
	}
	loadedPath := filepath.Join(root, "imported.jsonl")
	if err := WriteTasksJSONL(loadedPath, tasks); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadTasks(loadedPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ID != got.ID {
		t.Fatalf("loaded = %#v jsonl=%s", loaded, buf.String())
	}
}

func TestImportGenericTerminalBenchDataset(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "generic-task")
	if err := os.MkdirAll(filepath.Join(taskDir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(taskDir, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	taskYAML := `instruction: |-
  Fix the project.
  Run tests.
author_name: "someone"
difficulty: "easy"
tags:
  - "code"
  - "shell"
max_agent_timeout_sec: 45.5
`
	if err := os.WriteFile(filepath.Join(taskDir, "task.yaml"), []byte(taskYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "run-tests.sh"), []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	tasks, err := ImportTerminalBenchDataset(TerminalBenchImportOptions{DatasetDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %#v", tasks)
	}
	got := tasks[0]
	if got.ID != "generic-task" || got.Suite != TerminalBenchSuite {
		t.Fatalf("task identity = %#v", got)
	}
	if got.Prompt != "Fix the project.\nRun tests." {
		t.Fatalf("prompt = %q", got.Prompt)
	}
	if got.TimeoutSeconds != 46 {
		t.Fatalf("timeout = %d", got.TimeoutSeconds)
	}
	if got.WorkspaceTemplate != filepath.Join(taskDir, "workspace") {
		t.Fatalf("workspace template = %q", got.WorkspaceTemplate)
	}
	if !containsAll(got.Tags, TerminalBenchSuite, "code", "shell") {
		t.Fatalf("tags = %#v", got.Tags)
	}
	if len(got.Evaluator) != 3 || got.Evaluator[0] != "sh" || !strings.Contains(got.Evaluator[2], "run-tests.sh") {
		t.Fatalf("evaluator = %#v", got.Evaluator)
	}
}

func runTerminalBenchBridge(taskDir, appDir string) (string, error) {
	cmd := exec.Command("bash", filepath.Join(taskDir, "run-tests.sh"))
	cmd.Env = append(os.Environ(),
		"TEST_DIR="+filepath.Join(taskDir, "tests"),
		"APP_DIR="+appDir,
	)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func readText(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes)
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func containsAll(values []string, wants ...string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, want := range wants {
		if !seen[want] {
			return false
		}
	}
	return true
}
