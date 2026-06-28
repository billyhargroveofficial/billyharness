package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	LocalLongLoopSuite = "local-long-loop"
	localLoopTaskCount = 5
	defaultLoopTurns   = 60
	minLoopTurns       = 50
	maxLoopTurns       = 100
)

type LocalLoopOptions struct {
	TasksPath string
	Turns     int
	LiveWeb   bool
}

type LocalLoopSummary struct {
	TasksPath         string   `json:"tasks_path"`
	WorkspaceTemplate string   `json:"workspace_template"`
	Tasks             int      `json:"tasks"`
	ExpectedTurns     int      `json:"expected_turns"`
	ScriptedRounds    int      `json:"scripted_rounds"`
	TaskIDs           []string `json:"task_ids"`
}

func WriteLocalLoopTasks(opts LocalLoopOptions) (LocalLoopSummary, error) {
	if opts.TasksPath == "" {
		return LocalLoopSummary{}, fmt.Errorf("tasks path required")
	}
	turns := normalizeLoopTurns(opts.Turns)
	templateDir := filepath.Join(filepath.Dir(opts.TasksPath), "local-loop-template")
	if err := writeLocalLoopTemplate(templateDir); err != nil {
		return LocalLoopSummary{}, err
	}
	tasks := localLoopTasks(turns, templateDir, opts.LiveWeb)
	if err := WriteTasksJSONL(opts.TasksPath, tasks); err != nil {
		return LocalLoopSummary{}, err
	}
	summary := LocalLoopSummary{
		TasksPath:         opts.TasksPath,
		WorkspaceTemplate: templateDir,
		Tasks:             len(tasks),
		ExpectedTurns:     turns,
		TaskIDs:           make([]string, 0, len(tasks)),
	}
	for _, task := range tasks {
		summary.ScriptedRounds += task.ScriptedToolRounds
		summary.TaskIDs = append(summary.TaskIDs, task.ID)
	}
	return summary, nil
}

func LocalLoopTasks(turns int, templateDir string) []Task {
	return localLoopTasks(turns, templateDir, false)
}

func localLoopTasks(turns int, templateDir string, liveWeb bool) []Task {
	turns = normalizeLoopTurns(turns)
	scriptedRounds := max(localLoopTaskCount, turns-localLoopTaskCount)
	appRounds := max(10, scriptedRounds*30/100)
	fileRounds := max(8, scriptedRounds*20/100)
	webRounds := max(6, scriptedRounds*12/100)
	mcpRounds := max(6, scriptedRounds*12/100)
	cancelRounds := scriptedRounds - appRounds - fileRounds - webRounds - mcpRounds
	if cancelRounds < 1 {
		cancelRounds = 1
	}
	webToolName := "tool_search"
	webToolArgs := jsonArgs(map[string]any{"query": "web_search output caps", "limit": 5, "include_schema": false})
	webTags := []string{"web-search", "output-caps", "tool-discovery"}
	if liveWeb {
		webToolName = "web_search"
		webToolArgs = jsonArgs(map[string]any{"query": "billyharness local benchmark output caps", "limit": 3})
		webTags = []string{"web-search", "output-caps", "live-network"}
	}
	return []Task{
		{
			ID:                 "local-loop-app-build",
			Suite:              LocalLongLoopSuite,
			Prompt:             "Build a tiny local app artifact and keep edits bounded.",
			WorkspaceTemplate:  templateDir,
			Tags:               []string{"app-building", "file-edit", "dangerous-tools"},
			ScriptedToolRounds: appRounds,
			ScriptedToolName:   "fs_write_file",
			ScriptedToolArgs:   jsonArgs(map[string]any{"path": "app/generated.txt", "content": "local loop generated line\n", "append": true, "create_dirs": true}),
			TimeoutSeconds:     120,
		},
		{
			ID:                 "local-loop-file-search",
			Suite:              LocalLongLoopSuite,
			Prompt:             "Inspect local files and search for seeded benchmark markers.",
			WorkspaceTemplate:  templateDir,
			Tags:               []string{"file-edit", "file-search"},
			ScriptedToolRounds: fileRounds,
			ScriptedToolName:   "fs_search",
			ScriptedToolArgs:   jsonArgs(map[string]any{"path": ".", "query": "LOCAL_LOOP_MARKER", "limit": 20}),
			TimeoutSeconds:     120,
		},
		{
			ID:                 "local-loop-web-caps",
			Suite:              LocalLongLoopSuite,
			Prompt:             "Exercise web-search tool discovery and output cap policy without dumping raw pages.",
			WorkspaceTemplate:  templateDir,
			Tags:               webTags,
			ScriptedToolRounds: webRounds,
			ScriptedToolName:   webToolName,
			ScriptedToolArgs:   webToolArgs,
			TimeoutSeconds:     120,
		},
		{
			ID:                 "local-loop-mcp-discovery",
			Suite:              LocalLongLoopSuite,
			Prompt:             "Exercise MCP/tool discovery surfaces without injecting every MCP schema.",
			WorkspaceTemplate:  templateDir,
			Tags:               []string{"mcp", "tool-discovery"},
			ScriptedToolRounds: mcpRounds,
			ScriptedToolName:   "tool_search",
			ScriptedToolArgs:   jsonArgs(map[string]any{"query": "mcp_list_tools", "limit": 5, "include_schema": false}),
			TimeoutSeconds:     120,
		},
		{
			ID:                 "local-loop-cancel-resume",
			Suite:              LocalLongLoopSuite,
			Prompt:             "Exercise long-loop cancellation/resume telemetry expectations over a harmless repeated tool.",
			WorkspaceTemplate:  templateDir,
			Tags:               []string{"cancel-resume", "long-loop"},
			ScriptedToolRounds: cancelRounds,
			ScriptedToolName:   "time_now",
			ScriptedToolArgs:   `{}`,
			TimeoutSeconds:     120,
		},
	}
}

func normalizeLoopTurns(turns int) int {
	if turns <= 0 {
		turns = defaultLoopTurns
	}
	if turns < minLoopTurns {
		return minLoopTurns
	}
	if turns > maxLoopTurns {
		return maxLoopTurns
	}
	return turns
}

func writeLocalLoopTemplate(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o700); err != nil {
		return err
	}
	files := map[string]string{
		filepath.Join(dir, "README.md"):       "# Local Loop Benchmark\n\nLOCAL_LOOP_MARKER: root\n",
		filepath.Join(dir, "app", "main.txt"): "LOCAL_LOOP_MARKER: app seed\n",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func jsonArgs(value map[string]any) string {
	body, _ := json.Marshal(value)
	return string(body)
}
