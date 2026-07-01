package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	diag "github.com/billyhargroveofficial/billyharness/internal/diagnostics"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tooloutput"
)

func (r *Registry) addDiagnostics() {
	if r == nil || !r.diagnostics.Enabled {
		return
	}
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "diagnostics_run",
			Description: "Run one configured diagnostic command and return bounded structured diagnostics. Available command names: " + strings.Join(r.diagnosticCommandNames(), ", ") + ". Raw command output is stored out of band.",
			Parameters:  raw(`{"type":"object","properties":{"name":{"type":"string","description":"Configured diagnostic command name. Defaults to the first configured command."},"max_issues":{"type":"integer","default":100,"description":"Maximum parsed issues to return; clamped to the command cap."},"max_issues_per_file":{"type":"integer","default":20,"description":"Maximum parsed issues per file; clamped to the command cap."}},"additionalProperties":false}`),
			Risk:        protocol.RiskExecute,
		},
		Handler: r.handleDiagnosticsRun,
	})
}

func (r *Registry) handleDiagnosticsRun(ctx context.Context, args json.RawMessage) (Result, error) {
	if strings.TrimSpace(r.diagnosticsErr) != "" {
		err := fmt.Errorf("diagnostics config error: %s", r.diagnosticsErr)
		return errorResult("diagnostics_config_error", err.Error()), err
	}
	var in struct {
		Name             string `json:"name"`
		MaxIssues        int    `json:"max_issues"`
		MaxIssuesPerFile int    `json:"max_issues_per_file"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	command, err := r.diagnosticCommand(in.Name)
	if err != nil {
		return errorResult("diagnostics_unknown_command", err.Error()), err
	}
	cwd := command.CWD
	if cwd == "" {
		cwd = "."
	}
	safeCWD, err := r.safePath(cwd)
	if err != nil {
		return Result{}, err
	}
	command.CWD = safeCWD
	if in.MaxIssues > 0 && in.MaxIssues < command.MaxIssues {
		command.MaxIssues = in.MaxIssues
	}
	if in.MaxIssuesPerFile > 0 && in.MaxIssuesPerFile < command.MaxIssuesPerFile {
		command.MaxIssuesPerFile = in.MaxIssuesPerFile
	}
	result, err := diag.Run(ctx, diag.RunRequest{Command: toDiagnosticsCommand(command)})
	if err != nil {
		return Result{}, err
	}
	metadata := diag.Metadata(result, "")
	if summary := diagnosticsDisplaySummary(result); summary != "" {
		metadata["display_summary"] = summary
	}
	metadata["display_target"] = result.Name
	metadata["duration_ms"] = result.DurationMS
	metadata["original_output_bytes"] = result.OriginalOutputBytes
	metadata["truncated"] = result.OutputTruncated || result.IssuesTruncated

	var outputRef string
	if result.RawOutput != "" {
		ref, err := tooloutput.Store(tooloutput.StoreRequest{
			Parts:                 []string{"diagnostics", result.Name},
			Content:               result.RawOutput,
			EnsureTrailingNewline: true,
		})
		if err != nil {
			return Result{}, err
		}
		outputRef = ref.Path
		ref.AddMetadata(metadata)
		metadata["diagnostics_output_ref"] = outputRef
	}
	return Result{
		Content:   diag.Format(result, outputRef),
		Metadata:  metadata,
		Truncated: result.OutputTruncated || result.IssuesTruncated,
		OutputRef: outputRef,
	}, nil
}

func (r *Registry) diagnosticCommand(name string) (config.DiagnosticCommand, error) {
	commands := r.diagnostics.Commands
	if len(commands) == 0 {
		return config.DiagnosticCommand{}, fmt.Errorf("no diagnostic commands configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return commands[0], nil
	}
	name = strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	for _, command := range commands {
		if command.Name == name {
			return command, nil
		}
	}
	return config.DiagnosticCommand{}, fmt.Errorf("unknown diagnostic command %q; available: %s", name, strings.Join(r.diagnosticCommandNames(), ", "))
}

func (r *Registry) diagnosticCommandNames() []string {
	names := make([]string, 0, len(r.diagnostics.Commands))
	for _, command := range r.diagnostics.Commands {
		if command.Name != "" {
			names = append(names, command.Name)
		}
	}
	sort.Strings(names)
	return names
}

func toDiagnosticsCommand(command config.DiagnosticCommand) diag.Command {
	return diag.Command{
		Name:             command.Name,
		Command:          command.Command,
		Args:             append([]string(nil), command.Args...),
		CWD:              command.CWD,
		Timeout:          command.Timeout,
		MaxOutputBytes:   command.MaxOutputBytes,
		MaxIssues:        command.MaxIssues,
		MaxIssuesPerFile: command.MaxIssuesPerFile,
	}
}

func diagnosticsDisplaySummary(result diag.Result) string {
	status := "passed"
	if result.TimedOut {
		status = "timed out"
	} else if result.ExitCode != 0 {
		status = "failed"
	}
	if len(result.Issues) == 0 {
		return fmt.Sprintf("%s %s: no issues", result.Name, status)
	}
	return fmt.Sprintf("%s %s: %d issue%s (%d error%s, %d warning%s)",
		result.Name,
		status,
		len(result.Issues),
		pluralSuffix(len(result.Issues)),
		result.ErrorCount,
		pluralSuffix(result.ErrorCount),
		result.WarningCount,
		pluralSuffix(result.WarningCount),
	)
}
