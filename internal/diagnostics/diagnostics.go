package diagnostics

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxOutputBytes   = 256 * 1024
	DefaultMaxIssues        = 100
	DefaultMaxIssuesPerFile = 20
)

type Command struct {
	Name             string
	Command          string
	Args             []string
	CWD              string
	Timeout          time.Duration
	MaxOutputBytes   int
	MaxIssues        int
	MaxIssuesPerFile int
}

type Issue struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Raw      bool   `json:"raw,omitempty"`
}

type Result struct {
	Name                string
	Argv                []string
	CWD                 string
	ExitCode            int
	ExitError           string
	DurationMS          int64
	TimedOut            bool
	RawOutput           string
	OriginalOutputBytes int64
	OutputTruncated     bool
	Issues              []Issue
	IssuesTruncated     bool
	ErrorCount          int
	WarningCount        int
}

type RunRequest struct {
	Command Command
}

var (
	colonDiagnosticRE = regexp.MustCompile(`^(.+?):(\d+):(?:(\d+):)?\s*(?:(error|warning|warn|note|info|fatal):?\s*)?(.*)$`)
	parenDiagnosticRE = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\):\s*(?:(error|warning|warn|note|info|fatal)\s*)?(.*)$`)
)

func Run(ctx context.Context, req RunRequest) (Result, error) {
	command := normalizeCommand(req.Command)
	if command.Name == "" {
		return Result{}, fmt.Errorf("diagnostic command name required")
	}
	if command.Command == "" {
		return Result{}, fmt.Errorf("diagnostic command %q has no command", command.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, command.Timeout)
	defer cancel()

	started := time.Now()
	cmd := exec.CommandContext(ctx, command.Command, command.Args...)
	cmd.Dir = command.CWD
	buffer := &boundedBuffer{limit: command.MaxOutputBytes}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	err := cmd.Run()

	result := Result{
		Name:                command.Name,
		Argv:                append([]string{command.Command}, command.Args...),
		CWD:                 command.CWD,
		ExitCode:            0,
		DurationMS:          time.Since(started).Milliseconds(),
		TimedOut:            ctx.Err() == context.DeadlineExceeded,
		RawOutput:           buffer.String(),
		OriginalOutputBytes: buffer.OriginalBytes(),
		OutputTruncated:     buffer.Truncated(),
	}
	if err != nil {
		result.ExitCode = -1
		result.ExitError = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
	}
	result.Issues, result.IssuesTruncated = Parse(result.RawOutput, ParseOptions{
		MaxIssues:         command.MaxIssues,
		MaxIssuesPerFile:  command.MaxIssuesPerFile,
		FallbackOnFailure: result.ExitCode != 0 || result.TimedOut,
	})
	for _, issue := range result.Issues {
		switch issue.Severity {
		case "warning":
			result.WarningCount++
		default:
			result.ErrorCount++
		}
	}
	return result, nil
}

type ParseOptions struct {
	MaxIssues         int
	MaxIssuesPerFile  int
	FallbackOnFailure bool
}

func Parse(output string, opts ParseOptions) ([]Issue, bool) {
	opts = normalizeParseOptions(opts)
	var issues []Issue
	perFile := map[string]int{}
	truncated := false
	for _, line := range strings.Split(output, "\n") {
		issue, ok := parseLine(line)
		if !ok {
			continue
		}
		if issue.File != "" {
			if perFile[issue.File] >= opts.MaxIssuesPerFile {
				truncated = true
				continue
			}
			perFile[issue.File]++
		}
		issues = append(issues, issue)
		if len(issues) >= opts.MaxIssues {
			return sortIssues(issues), true
		}
	}
	if len(issues) == 0 && opts.FallbackOnFailure {
		if raw := firstNonEmptyLine(output); raw != "" {
			issues = append(issues, Issue{
				Severity: "error",
				Message:  compact(raw, 240),
				Raw:      true,
			})
		}
	}
	return sortIssues(issues), truncated
}

func Format(result Result, outputRef string) string {
	status := "passed"
	if result.TimedOut {
		status = "timed_out"
	} else if result.ExitCode != 0 {
		status = "failed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<diagnostics name=%q status=%q exit_code=%d issues=%d errors=%d warnings=%d", result.Name, status, result.ExitCode, len(result.Issues), result.ErrorCount, result.WarningCount)
	if outputRef != "" {
		fmt.Fprintf(&b, " output_ref=%q", outputRef)
	}
	if result.OutputTruncated {
		b.WriteString(" output_truncated=\"true\"")
	}
	b.WriteString(">\n")
	limit := len(result.Issues)
	if limit > 40 {
		limit = 40
	}
	for _, issue := range result.Issues[:limit] {
		fmt.Fprintf(&b, "%s %s\n", issue.Severity, formatIssueLocation(issue))
		if issue.Message != "" {
			b.WriteString("  ")
			b.WriteString(compact(issue.Message, 240))
			b.WriteByte('\n')
		}
	}
	if len(result.Issues) > limit {
		fmt.Fprintf(&b, "... %d additional issue(s) truncated\n", len(result.Issues)-limit)
	} else if result.IssuesTruncated {
		b.WriteString("... additional issue(s) truncated\n")
	}
	if outputRef != "" {
		fmt.Fprintf(&b, "raw_output_ref: %s\n", outputRef)
	}
	b.WriteString("</diagnostics>")
	return b.String()
}

func Metadata(result Result, outputRef string) map[string]any {
	metadata := map[string]any{
		"diagnostics_name":                  result.Name,
		"diagnostics_argv":                  append([]string(nil), result.Argv...),
		"diagnostics_cwd":                   result.CWD,
		"diagnostics_exit_code":             result.ExitCode,
		"diagnostics_duration_ms":           result.DurationMS,
		"diagnostics_timed_out":             result.TimedOut,
		"diagnostics_issue_count":           len(result.Issues),
		"diagnostics_error_count":           result.ErrorCount,
		"diagnostics_warning_count":         result.WarningCount,
		"diagnostics_issues_truncated":      result.IssuesTruncated,
		"diagnostics_output_truncated":      result.OutputTruncated,
		"diagnostics_original_output_bytes": result.OriginalOutputBytes,
		"diagnostics_issues":                result.Issues,
	}
	if result.ExitError != "" {
		metadata["diagnostics_exit_error"] = result.ExitError
	}
	if outputRef != "" {
		metadata["diagnostics_output_ref"] = outputRef
	}
	return metadata
}

func normalizeCommand(command Command) Command {
	command.Name = strings.TrimSpace(command.Name)
	command.Command = strings.TrimSpace(command.Command)
	command.CWD = strings.TrimSpace(command.CWD)
	if command.Timeout <= 0 {
		command.Timeout = 120 * time.Second
	}
	if command.MaxOutputBytes <= 0 {
		command.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if command.MaxIssues <= 0 {
		command.MaxIssues = DefaultMaxIssues
	}
	if command.MaxIssuesPerFile <= 0 {
		command.MaxIssuesPerFile = DefaultMaxIssuesPerFile
	}
	return command
}

func normalizeParseOptions(opts ParseOptions) ParseOptions {
	if opts.MaxIssues <= 0 {
		opts.MaxIssues = DefaultMaxIssues
	}
	if opts.MaxIssuesPerFile <= 0 {
		opts.MaxIssuesPerFile = DefaultMaxIssuesPerFile
	}
	return opts
}

func parseLine(line string) (Issue, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Issue{}, false
	}
	if match := parenDiagnosticRE.FindStringSubmatch(line); match != nil {
		return issueFromMatch(match[1], atoi(match[2]), atoi(match[3]), match[4], match[5]), true
	}
	if match := colonDiagnosticRE.FindStringSubmatch(line); match != nil {
		return issueFromMatch(match[1], atoi(match[2]), atoi(match[3]), match[4], match[5]), true
	}
	return Issue{}, false
}

func issueFromMatch(file string, line int, col int, severity string, message string) Issue {
	return Issue{
		File:     cleanFile(file),
		Line:     line,
		Column:   col,
		Severity: normalizeSeverity(severity),
		Message:  strings.TrimSpace(message),
	}
}

func normalizeSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "warning", "warn", "note", "info":
		return "warning"
	default:
		return "error"
	}
}

func cleanFile(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "./")
	return value
}

func sortIssues(issues []Issue) []Issue {
	sort.SliceStable(issues, func(i, j int) bool {
		left := severityRank(issues[i].Severity)
		right := severityRank(issues[j].Severity)
		if left != right {
			return left < right
		}
		if issues[i].File != issues[j].File {
			return issues[i].File < issues[j].File
		}
		if issues[i].Line != issues[j].Line {
			return issues[i].Line < issues[j].Line
		}
		return issues[i].Column < issues[j].Column
	})
	return issues
}

func severityRank(value string) int {
	if value == "warning" {
		return 1
	}
	return 0
}

func formatIssueLocation(issue Issue) string {
	if issue.File == "" {
		return "(raw)"
	}
	if issue.Line <= 0 {
		return issue.File
	}
	if issue.Column > 0 {
		return fmt.Sprintf("%s:%d:%d", issue.File, issue.Line, issue.Column)
	}
	return fmt.Sprintf("%s:%d", issue.File, issue.Line)
}

func firstNonEmptyLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func compact(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func atoi(value string) int {
	var out int
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return out
		}
		out = out*10 + int(ch-'0')
	}
	return out
}

type boundedBuffer struct {
	mu      sync.Mutex
	limit   int
	buf     bytes.Buffer
	seen    int64
	dropped bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seen += int64(len(p))
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.dropped = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.dropped = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *boundedBuffer) OriginalBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seen
}

func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}
