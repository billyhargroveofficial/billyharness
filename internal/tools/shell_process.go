package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/tooloutput"
)

const (
	maxManagedShellProcesses = 8
	maxManagedShellBuffer    = 1024 * 1024
	shellTerminateGrace      = 200 * time.Millisecond
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
	if in.Background {
		return r.startManagedShell(in, cwd)
	}
	return runForegroundShell(ctx, in, cwd, explicitMaxOutput)
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
