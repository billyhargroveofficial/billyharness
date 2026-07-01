package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tooloutput"
)

const (
	maxManagedShellProcesses = 8
	maxManagedShellBuffer    = 1024 * 1024
	maxManagedShellList      = 50
	maxManagedShellPreview   = 240
	shellTerminateGrace      = 200 * time.Millisecond
)

var (
	managedShellURLRE      = regexp.MustCompile(`https?://[^\s<>"'()]+`)
	managedShellHostPortRE = regexp.MustCompile(`(?i)\b(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):(\d{2,5})\b`)
	managedShellPortRE     = regexp.MustCompile(`(?i)\b(?:port|listening on|serving on|server on)\D{0,24}(\d{2,5})\b`)
)

type shellExecInput struct {
	Argv           []string `json:"argv"`
	CWD            string   `json:"cwd"`
	TimeoutSec     int      `json:"timeout_sec"`
	MaxOutputBytes int      `json:"max_output_bytes"`
	Background     bool     `json:"background"`
}

type managedShellProcess struct {
	id        string
	argv      []string
	cwd       string
	startedAt time.Time
	cmd       *exec.Cmd
	output    shellOutputBuffer

	mu       sync.Mutex
	exited   bool
	exitCode int
	exitErr  string
	endedAt  time.Time

	lastOutputRef   string
	lastOutputRefID string
	lastOutputBytes int64
	lastOutputRefAt time.Time
}

type shellOutputBuffer struct {
	mu   sync.Mutex
	base int64
	next int64
	buf  []byte
}

type shellOutputSlice struct {
	Content     string
	BaseCursor  int64
	NextCursor  int64
	Dropped     int64
	Truncated   bool
	RetainedEnd int64
}

func (r *Registry) handleShellExec(ctx context.Context, args json.RawMessage) (Result, error) {
	in, explicitMaxOutput, err := parseShellExecInput(args)
	if err != nil {
		return Result{}, err
	}
	cwd, err := r.safePath(in.CWD)
	if err != nil {
		return Result{}, err
	}
	if reason, blocked := destructiveGitCommandReason(in.Argv); blocked {
		result := errorResult("destructive_git_command", reason)
		result.Metadata = map[string]any{
			"guardrail": "destructive_git",
			"argv":      append([]string(nil), in.Argv...),
		}
		return result, fmt.Errorf("%s", reason)
	}
	if in.Background {
		return r.startManagedShell(in, cwd)
	}
	return runForegroundShell(ctx, in, cwd, explicitMaxOutput)
}

func destructiveGitCommandReason(argv []string) (string, bool) {
	args, ok := gitArgsFromShellArgv(argv)
	if !ok || len(args) == 0 {
		return "", false
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]
	switch subcommand {
	case "reset":
		if hasLongFlag(rest, "--hard") {
			return "destructive git command blocked: git reset --hard would bypass checkpoint/redo safety", true
		}
	case "clean":
		if hasShortFlag(rest, 'f') || hasLongFlag(rest, "--force") {
			return "destructive git command blocked: git clean -f would bypass checkpoint/redo safety", true
		}
	case "checkout":
		if hasWorkspacePathspec(rest) {
			return "destructive git command blocked: git checkout . would bypass checkpoint/redo safety", true
		}
	case "restore":
		if hasWorkspacePathspec(rest) {
			return "destructive git command blocked: git restore . would bypass checkpoint/redo safety", true
		}
	case "stash":
		if len(rest) > 0 {
			switch strings.ToLower(strings.TrimSpace(rest[0])) {
			case "drop", "clear":
				return "destructive git command blocked: git stash " + strings.ToLower(strings.TrimSpace(rest[0])) + " would bypass checkpoint/redo safety", true
			}
		}
	case "push":
		if hasShortFlag(rest, 'f') || hasLongFlagPrefix(rest, "--force") {
			return "destructive git command blocked: git push --force would bypass checkpoint/redo safety", true
		}
	}
	return "", false
}

func gitArgsFromShellArgv(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	if isGitCommand(argv[0]) {
		return argv[1:], true
	}
	if !isShellCommand(argv[0]) {
		return nil, false
	}
	for i := 1; i < len(argv)-1; i++ {
		if shellOptionRunsCommand(argv[i]) {
			fields := strings.Fields(argv[i+1])
			if len(fields) > 0 && isGitCommand(fields[0]) {
				return fields[1:], true
			}
			return nil, false
		}
	}
	return nil, false
}

func isGitCommand(command string) bool {
	return strings.EqualFold(filepath.Base(strings.TrimSpace(command)), "git")
}

func isShellCommand(command string) bool {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(command))) {
	case "sh", "bash", "zsh", "dash":
		return true
	default:
		return false
	}
}

func shellOptionRunsCommand(arg string) bool {
	arg = strings.TrimSpace(arg)
	return strings.HasPrefix(arg, "-") && strings.Contains(arg[1:], "c")
}

func hasLongFlag(args []string, flag string) bool {
	for _, arg := range args {
		if strings.EqualFold(strings.TrimSpace(arg), flag) {
			return true
		}
	}
	return false
}

func hasLongFlagPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if strings.EqualFold(arg, prefix) || strings.HasPrefix(strings.ToLower(arg), strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

func hasShortFlag(args []string, flag rune) bool {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if len(arg) < 2 || !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
			continue
		}
		for _, r := range arg[1:] {
			if r == flag {
				return true
			}
		}
	}
	return false
}

func hasWorkspacePathspec(args []string) bool {
	for _, arg := range args {
		switch strings.TrimSpace(arg) {
		case ".", ":/":
			return true
		}
	}
	return false
}

func parseShellExecInput(args json.RawMessage) (shellExecInput, bool, error) {
	var in shellExecInput
	if err := json.Unmarshal(args, &in); err != nil {
		return shellExecInput{}, false, err
	}
	if len(in.Argv) == 0 || strings.TrimSpace(in.Argv[0]) == "" {
		return shellExecInput{}, false, fmt.Errorf("argv required")
	}
	if in.CWD == "" {
		in.CWD = "."
	}
	if in.TimeoutSec <= 0 || in.TimeoutSec > 120 {
		in.TimeoutSec = 20
	}
	explicitMaxOutput := in.MaxOutputBytes > 0
	if in.MaxOutputBytes <= 0 || in.MaxOutputBytes > maxExecOutput {
		in.MaxOutputBytes = maxExecOutput
	}
	return in, explicitMaxOutput, nil
}

func runForegroundShell(ctx context.Context, in shellExecInput, cwd string, explicitMaxOutput bool) (Result, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(in.TimeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, in.Argv[0], in.Argv[1:]...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	text := string(output)
	if explicitMaxOutput {
		text = truncate(text, in.MaxOutputBytes)
	}
	if cmdCtx.Err() != nil {
		return Result{Content: text}, cmdCtx.Err()
	}
	if err != nil {
		return Result{Content: text}, fmt.Errorf("command failed: %w", err)
	}
	return Result{Content: text}, nil
}

func (r *Registry) startManagedShell(in shellExecInput, cwd string) (Result, error) {
	if r == nil {
		return Result{}, fmt.Errorf("tool registry unavailable")
	}
	r.shellMu.Lock()
	r.pruneManagedShellLocked()
	if r.runningManagedShellCountLocked() >= maxManagedShellProcesses {
		r.shellMu.Unlock()
		return Result{}, fmt.Errorf("maximum live shell processes reached: %d", maxManagedShellProcesses)
	}
	r.shellSeq++
	id := fmt.Sprintf("shell-%d", r.shellSeq)
	r.shellMu.Unlock()

	proc := &managedShellProcess{
		id:        id,
		argv:      append([]string(nil), in.Argv...),
		cwd:       cwd,
		startedAt: time.Now().UTC(),
	}
	cmd := exec.Command(in.Argv[0], in.Argv[1:]...)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = &proc.output
	cmd.Stderr = &proc.output
	proc.cmd = cmd
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	r.shellMu.Lock()
	r.shellProcesses[id] = proc
	r.shellMu.Unlock()
	go proc.wait()

	metadata := proc.metadata()
	metadata["process_id"] = id
	metadata["pid"] = cmd.Process.Pid
	metadata["running"] = true
	metadata["next_cursor"] = int64(0)
	return Result{
		Content:  fmt.Sprintf("started background shell %s pid=%d", id, cmd.Process.Pid),
		Metadata: metadata,
	}, nil
}

func (r *Registry) handleShellOutput(_ context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		ProcessID      string `json:"process_id"`
		Cursor         int64  `json:"cursor"`
		MaxOutputBytes int    `json:"max_output_bytes"`
		TailBytes      int    `json:"tail_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	if in.ProcessID == "" {
		return Result{}, fmt.Errorf("process_id required")
	}
	if in.MaxOutputBytes <= 0 || in.MaxOutputBytes > maxExecOutput {
		in.MaxOutputBytes = maxExecOutput
	}
	proc, err := r.managedShell(in.ProcessID)
	if err != nil {
		return Result{}, err
	}
	slice := proc.output.read(in.Cursor, in.MaxOutputBytes, in.TailBytes)
	metadata := proc.metadata()
	metadata["process_id"] = proc.id
	metadata["cursor"] = in.Cursor
	metadata["base_cursor"] = slice.BaseCursor
	metadata["next_cursor"] = slice.NextCursor
	metadata["retained_end_cursor"] = slice.RetainedEnd
	metadata["dropped_bytes"] = slice.Dropped
	metadata["truncated"] = slice.Truncated
	if in.TailBytes > 0 {
		metadata["tail_bytes"] = in.TailBytes
	}
	if strings.TrimSpace(slice.Content) != "" {
		ref, err := tooloutput.Store(tooloutput.StoreRequest{
			Parts:                 []string{"shell_output", proc.id},
			Content:               slice.Content,
			EnsureTrailingNewline: true,
		})
		if err == nil && ref.Path != "" {
			ref.AddMetadata(metadata)
			proc.setLastOutputRef(ref)
		}
	}
	return Result{Content: slice.Content, Metadata: metadata, Truncated: slice.Truncated, OutputRef: metadataStringValue(metadata, tooloutput.MetadataOutputRef)}, nil
}

func (r *Registry) handleShellKill(_ context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		ProcessID string `json:"process_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	if in.ProcessID == "" {
		return Result{}, fmt.Errorf("process_id required")
	}
	proc, err := r.managedShell(in.ProcessID)
	if err != nil {
		return Result{}, err
	}
	if proc.isExited() {
		metadata := proc.metadata()
		metadata["process_id"] = proc.id
		return Result{Content: "process already exited " + proc.id, Metadata: metadata}, nil
	}
	if err := terminateManagedShell(proc); err != nil {
		return Result{}, err
	}
	metadata := proc.metadata()
	metadata["process_id"] = proc.id
	return Result{Content: "terminated " + proc.id, Metadata: metadata}, nil
}

func (r *Registry) handleShellProcesses(_ context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		IncludeExited bool `json:"include_exited"`
		Limit         int  `json:"limit"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return Result{}, err
		}
	}
	processes := r.ManagedShellProcesses(in.IncludeExited, in.Limit)
	metadata := map[string]any{
		"managed_processes": processes,
		"process_count":     len(processes.Processes),
		"running":           processes.Running,
		"exited":            processes.Exited,
		"display_summary":   managedShellProcessSummary(processes),
		"display_group":     "shell_processes",
		"display_target":    "managed shell processes",
		"display_preview":   managedShellProcessPreview(processes),
		"collapse_default":  true,
	}
	return Result{Content: FormatManagedShellProcesses(processes), Metadata: metadata}, nil
}

func (r *Registry) ManagedShellProcesses(includeExited bool, limit int) protocol.ManagedProcessList {
	if limit <= 0 || limit > maxManagedShellList {
		limit = maxManagedShellList
	}
	now := time.Now().UTC()
	var procs []*managedShellProcess
	if r != nil {
		r.shellMu.Lock()
		procs = make([]*managedShellProcess, 0, len(r.shellProcesses))
		for _, proc := range r.shellProcesses {
			if proc != nil {
				procs = append(procs, proc)
			}
		}
		r.shellMu.Unlock()
	}
	sort.Slice(procs, func(i, j int) bool {
		return managedShellIDNumber(procs[i].id) < managedShellIDNumber(procs[j].id)
	})
	out := protocol.ManagedProcessList{
		GeneratedAt: now.Format(time.RFC3339Nano),
		Limit:       limit,
	}
	for _, proc := range procs {
		status := proc.status(now)
		if status.Running {
			out.Running++
		} else {
			out.Exited++
		}
		if !includeExited && !status.Running {
			continue
		}
		if len(out.Processes) >= limit {
			out.Truncated = true
			continue
		}
		out.Processes = append(out.Processes, status)
	}
	return out
}

func managedShellIDNumber(id string) int64 {
	id = strings.TrimPrefix(strings.TrimSpace(id), "shell-")
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 1<<62 - 1
	}
	return n
}

func (r *Registry) managedShell(id string) (*managedShellProcess, error) {
	if r == nil {
		return nil, fmt.Errorf("tool registry unavailable")
	}
	r.shellMu.Lock()
	defer r.shellMu.Unlock()
	proc := r.shellProcesses[strings.TrimSpace(id)]
	if proc == nil {
		return nil, fmt.Errorf("unknown shell process %s", id)
	}
	return proc, nil
}

func (r *Registry) runningManagedShellCountLocked() int {
	running := 0
	for _, proc := range r.shellProcesses {
		if proc != nil && !proc.isExited() {
			running++
		}
	}
	return running
}

func (r *Registry) pruneManagedShellLocked() {
	if len(r.shellProcesses) < maxManagedShellProcesses*2 {
		return
	}
	for id, proc := range r.shellProcesses {
		if proc == nil || proc.isExited() {
			delete(r.shellProcesses, id)
		}
	}
}

func (r *Registry) closeManagedShellProcesses() {
	if r == nil {
		return
	}
	r.shellMu.Lock()
	procs := make([]*managedShellProcess, 0, len(r.shellProcesses))
	for _, proc := range r.shellProcesses {
		if proc != nil {
			procs = append(procs, proc)
		}
	}
	r.shellMu.Unlock()
	for _, proc := range procs {
		if !proc.isExited() {
			_ = terminateManagedShell(proc)
		}
	}
}

func (p *managedShellProcess) wait() {
	err := p.cmd.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exited = true
	p.endedAt = time.Now().UTC()
	p.exitCode = 0
	if err != nil {
		p.exitErr = err.Error()
		p.exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			p.exitCode = exitErr.ExitCode()
		}
	}
}

func (p *managedShellProcess) isExited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

func (p *managedShellProcess) metadata() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	metadata := map[string]any{
		"argv":       append([]string(nil), p.argv...),
		"cwd":        p.cwd,
		"started_at": p.startedAt.Format(time.RFC3339Nano),
		"running":    !p.exited,
		"exited":     p.exited,
	}
	if p.cmd != nil && p.cmd.Process != nil {
		metadata["pid"] = p.cmd.Process.Pid
	}
	if p.exited {
		metadata["exit_code"] = p.exitCode
		metadata["ended_at"] = p.endedAt.Format(time.RFC3339Nano)
		if p.exitErr != "" {
			metadata["exit_error"] = p.exitErr
		}
	}
	return metadata
}

func (p *managedShellProcess) setLastOutputRef(ref tooloutput.Ref) {
	if p == nil || ref.Path == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastOutputRef = ref.Path
	p.lastOutputRefID = ref.ID
	p.lastOutputBytes = ref.Bytes
	p.lastOutputRefAt = time.Now().UTC()
}

func (p *managedShellProcess) status(now time.Time) protocol.ManagedProcessStatus {
	p.mu.Lock()
	id := p.id
	argv := append([]string(nil), p.argv...)
	cwd := p.cwd
	startedAt := p.startedAt
	cmd := p.cmd
	exited := p.exited
	exitCode := p.exitCode
	exitErr := p.exitErr
	endedAt := p.endedAt
	outputRef := p.lastOutputRef
	outputRefID := p.lastOutputRefID
	outputRefBytes := p.lastOutputBytes
	outputRefAt := p.lastOutputRefAt
	p.mu.Unlock()

	slice := p.output.tail(maxManagedShellPreview)
	ports, urls := detectManagedShellEndpoints(slice.Content)
	end := now
	if exited && !endedAt.IsZero() {
		end = endedAt
	}
	elapsed := end.Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	status := protocol.ManagedProcessStatus{
		ID:                id,
		Kind:              "shell",
		Argv:              argv,
		Command:           strings.Join(argv, " "),
		CWD:               cwd,
		Running:           !exited,
		Exited:            exited,
		StartedAt:         startedAt.Format(time.RFC3339Nano),
		ElapsedMS:         elapsed.Milliseconds(),
		RetainedBytes:     slice.RetainedEnd - slice.BaseCursor,
		BaseCursor:        slice.BaseCursor,
		NextCursor:        slice.RetainedEnd,
		DroppedBytes:      slice.BaseCursor,
		OutputRef:         outputRef,
		OutputRefID:       outputRefID,
		OutputRefBytes:    outputRefBytes,
		DetectedPorts:     ports,
		DetectedURLs:      urls,
		OutputTailPreview: compactManagedShellPreview(slice.Content),
	}
	if cmd != nil && cmd.Process != nil {
		status.PID = cmd.Process.Pid
	}
	if exited {
		status.ExitCode = exitCode
		status.EndedAt = endedAt.Format(time.RFC3339Nano)
		status.ExitError = exitErr
	}
	if !outputRefAt.IsZero() {
		status.OutputRefAt = outputRefAt.Format(time.RFC3339Nano)
	}
	return status
}

func FormatManagedShellProcesses(processes protocol.ManagedProcessList) string {
	if len(processes.Processes) == 0 {
		if processes.Running == 0 && processes.Exited == 0 {
			return "no managed shell processes"
		}
		return fmt.Sprintf("managed shell processes: %d running, %d exited (none shown)", processes.Running, processes.Exited)
	}
	lines := []string{fmt.Sprintf("managed shell processes: %d running, %d exited", processes.Running, processes.Exited)}
	for _, proc := range processes.Processes {
		lines = append(lines, "- "+managedShellProcessLine(proc))
	}
	if processes.Truncated {
		lines = append(lines, fmt.Sprintf("... truncated at %d processes", processes.Limit))
	}
	return strings.Join(lines, "\n")
}

func managedShellProcessSummary(processes protocol.ManagedProcessList) string {
	return fmt.Sprintf("managed shell processes %d running %d exited", processes.Running, processes.Exited)
}

func managedShellProcessPreview(processes protocol.ManagedProcessList) string {
	if len(processes.Processes) == 0 {
		return "no managed shell processes"
	}
	var parts []string
	for i, proc := range processes.Processes {
		if i >= 3 {
			break
		}
		parts = append(parts, proc.ID+" "+managedShellProcessState(proc))
	}
	return strings.Join(parts, ", ")
}

func managedShellProcessLine(proc protocol.ManagedProcessStatus) string {
	parts := []string{proc.ID, managedShellProcessState(proc)}
	if proc.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid=%d", proc.PID))
	}
	if proc.ElapsedMS > 0 {
		parts = append(parts, "elapsed="+compactManagedShellDuration(proc.ElapsedMS))
	}
	if proc.CWD != "" {
		parts = append(parts, "cwd="+proc.CWD)
	}
	if proc.Command != "" {
		parts = append(parts, "cmd="+truncate(proc.Command, 120))
	}
	if len(proc.DetectedPorts) > 0 {
		parts = append(parts, "ports="+joinPorts(proc.DetectedPorts))
	}
	if len(proc.DetectedURLs) > 0 {
		parts = append(parts, "urls="+strings.Join(proc.DetectedURLs, ","))
	}
	if proc.OutputRef != "" {
		parts = append(parts, "output_ref="+proc.OutputRef)
	}
	parts = append(parts, fmt.Sprintf("cursor=%d", proc.NextCursor))
	if proc.OutputTailPreview != "" {
		parts = append(parts, "tail="+strconv.Quote(proc.OutputTailPreview))
	}
	return strings.Join(parts, " ")
}

func managedShellProcessState(proc protocol.ManagedProcessStatus) string {
	if proc.Running {
		return "running"
	}
	if proc.ExitError != "" {
		return fmt.Sprintf("exited(%d)", proc.ExitCode)
	}
	if proc.Exited {
		return fmt.Sprintf("exited(%d)", proc.ExitCode)
	}
	return "unknown"
}

func compactManagedShellDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.1fm", d.Minutes())
	case d >= time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dms", ms)
	}
}

func joinPorts(ports []int) string {
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ",")
}

func compactManagedShellPreview(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	return truncate(text, maxManagedShellPreview)
}

func detectManagedShellEndpoints(text string) ([]int, []string) {
	portsSeen := map[int]bool{}
	var ports []int
	addPort := func(value string) {
		port, err := strconv.Atoi(value)
		if err != nil || port <= 0 || port > 65535 || portsSeen[port] {
			return
		}
		portsSeen[port] = true
		ports = append(ports, port)
	}
	for _, match := range managedShellHostPortRE.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			addPort(match[1])
		}
	}
	for _, match := range managedShellPortRE.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			addPort(match[1])
		}
	}
	urlSeen := map[string]bool{}
	var urls []string
	for _, rawURL := range managedShellURLRE.FindAllString(text, -1) {
		clean := strings.TrimRight(rawURL, ".,;:")
		if clean == "" || urlSeen[clean] {
			continue
		}
		urlSeen[clean] = true
		urls = append(urls, clean)
	}
	if len(ports) > 6 {
		ports = ports[:6]
	}
	if len(urls) > 6 {
		urls = urls[:6]
	}
	return ports, urls
}

func (b *shellOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	b.next += int64(len(p))
	if len(b.buf) > maxManagedShellBuffer {
		drop := len(b.buf) - maxManagedShellBuffer
		b.buf = append([]byte(nil), b.buf[drop:]...)
		b.base += int64(drop)
	}
	return len(p), nil
}

func (b *shellOutputBuffer) read(cursor int64, maxBytes int, tailBytes int) shellOutputSlice {
	b.mu.Lock()
	defer b.mu.Unlock()
	if maxBytes <= 0 || maxBytes > maxExecOutput {
		maxBytes = maxExecOutput
	}
	start := cursor
	dropped := int64(0)
	if tailBytes > 0 {
		if tailBytes > len(b.buf) {
			tailBytes = len(b.buf)
		}
		start = b.next - int64(tailBytes)
	} else {
		if start < b.base {
			dropped = b.base - start
			start = b.base
		}
		if start > b.next {
			start = b.next
		}
	}
	offset := int(start - b.base)
	if offset < 0 {
		offset = 0
	}
	if offset > len(b.buf) {
		offset = len(b.buf)
	}
	out := b.buf[offset:]
	truncated := false
	if len(out) > maxBytes {
		out = out[:maxBytes]
		truncated = true
	}
	next := start + int64(len(out))
	return shellOutputSlice{
		Content:     string(out),
		BaseCursor:  b.base,
		NextCursor:  next,
		Dropped:     dropped,
		Truncated:   truncated,
		RetainedEnd: b.next,
	}
}

func (b *shellOutputBuffer) tail(maxBytes int) shellOutputSlice {
	return b.read(0, maxBytes, maxBytes)
}

func terminateManagedShell(proc *managedShellProcess) error {
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return nil
	}
	pid := proc.cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = proc.cmd.Process.Kill()
	}
	deadline := time.Now().Add(shellTerminateGrace)
	for time.Now().Before(deadline) {
		if proc.isExited() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if proc.isExited() {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return proc.cmd.Process.Kill()
	}
	return nil
}

func metadataStringValue(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(string); ok {
		return value
	}
	return ""
}
