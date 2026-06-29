package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	hygieneGoFileLineLimit     = 1500
	hygieneGoTestFileLineLimit = 1200
)

var hygieneRuntimeArtifactPaths = []string{
	".env",
	"auth",
	"bench-runs",
	"bin",
	"fast-agent-harness",
	"gateway-sessions",
	"gateway.log",
	"gateway.pid",
	"mcp.config.toml",
	"sessions",
	"settings.json",
	"telegram",
	"telegram.log",
	"telegram.pid",
	"tool-output",
	"web-cache",
}

type hygieneOptions struct {
	RepoDir string
	JSON    bool
	Strict  bool
	Timeout time.Duration
}

type hygieneReport struct {
	GeneratedAt      string                   `json:"generated_at"`
	RepoDir          string                   `json:"repo_dir"`
	Source           hygieneSourceReport      `json:"source"`
	RuntimeArtifacts []hygieneRuntimeArtifact `json:"runtime_artifacts"`
}

type hygieneSourceReport struct {
	TrackedGoFiles    int                `json:"tracked_go_files"`
	LargeFiles        []hygieneLargeFile `json:"large_files,omitempty"`
	AllowedLargeFiles []hygieneLargeFile `json:"allowed_large_files,omitempty"`
	MissingFiles      []string           `json:"missing_files,omitempty"`
}

type hygieneLargeFile struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
	Limit int    `json:"limit"`
	Kind  string `json:"kind"`
}

type hygieneRuntimeArtifact struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes,omitempty"`
	Error  string `json:"error,omitempty"`
}

func hygieneCmd(args []string) error {
	return hygieneCommand(args, os.Stdout, osDoctorRunner{})
}

func hygieneCommand(args []string, out io.Writer, runner doctorCommandRunner) error {
	fs := flag.NewFlagSet("hygiene", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	strict := fs.Bool("strict", false, "exit non-zero when source files exceed size budgets or tracked files are missing")
	repoDir := fs.String("repo", "", "repository directory; defaults to current git root")
	timeoutSec := fs.Int("timeout-sec", 10, "per-command timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := hygieneOptions{
		RepoDir: strings.TrimSpace(*repoDir),
		JSON:    *jsonOut,
		Strict:  *strict,
		Timeout: time.Duration(*timeoutSec) * time.Second,
	}
	report, err := collectHygieneReport(context.Background(), opts, runner)
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printHygieneReport(out, report)
	}
	if opts.Strict {
		if err := hygieneStrictError(report.Source); err != nil {
			return err
		}
	}
	return nil
}

func hygieneStrictError(source hygieneSourceReport) error {
	var issues []string
	if len(source.LargeFiles) > 0 {
		issues = append(issues, fmt.Sprintf("%d large source files", len(source.LargeFiles)))
	}
	if len(source.MissingFiles) > 0 {
		issues = append(issues, fmt.Sprintf("%d missing tracked Go files", len(source.MissingFiles)))
	}
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("hygiene found %s", strings.Join(issues, " and "))
}

func collectHygieneReport(ctx context.Context, opts hygieneOptions, runner doctorCommandRunner) (hygieneReport, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	repoDir := opts.RepoDir
	if repoDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return hygieneReport{}, err
		}
		out, err := runDoctorCommand(ctx, runner, cwd, opts.Timeout, "git", "rev-parse", "--show-toplevel")
		if err != nil {
			return hygieneReport{}, fmt.Errorf("resolve git root: %w", err)
		}
		repoDir = strings.TrimSpace(out)
	}
	if repoDir == "" {
		return hygieneReport{}, errors.New("repository directory is empty")
	}
	source, err := collectHygieneSource(ctx, repoDir, opts, runner)
	if err != nil {
		return hygieneReport{}, err
	}
	return hygieneReport{
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		RepoDir:          repoDir,
		Source:           source,
		RuntimeArtifacts: collectHygieneRuntimeArtifacts(repoDir),
	}, nil
}

func collectHygieneSource(ctx context.Context, repoDir string, opts hygieneOptions, runner doctorCommandRunner) (hygieneSourceReport, error) {
	out, err := runDoctorCommand(ctx, runner, repoDir, opts.Timeout, "git", "ls-files", "--", "*.go")
	if err != nil {
		return hygieneSourceReport{}, fmt.Errorf("git ls-files: %w", err)
	}
	largeFileAllowlist := hygieneLargeFileAllowlist(repoDir)
	var report hygieneSourceReport
	for _, path := range strings.Split(out, "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		report.TrackedGoFiles++
		fullPath := filepath.Join(repoDir, filepath.FromSlash(path))
		body, err := os.ReadFile(fullPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				report.MissingFiles = append(report.MissingFiles, path)
				continue
			}
			return hygieneSourceReport{}, fmt.Errorf("read %s: %w", path, err)
		}
		if generatedGoFile(body) {
			continue
		}
		limit, kind := hygieneLineLimit(path)
		lines := countLines(body)
		if lines > limit {
			large := hygieneLargeFile{
				Path:  path,
				Lines: lines,
				Limit: limit,
				Kind:  kind,
			}
			if largeFileAllowlist[path] {
				report.AllowedLargeFiles = append(report.AllowedLargeFiles, large)
			} else {
				report.LargeFiles = append(report.LargeFiles, large)
			}
		}
	}
	sortLargeFiles(report.LargeFiles)
	sortLargeFiles(report.AllowedLargeFiles)
	sort.Strings(report.MissingFiles)
	return report, nil
}

func sortLargeFiles(files []hygieneLargeFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].Lines == files[j].Lines {
			return files[i].Path < files[j].Path
		}
		return files[i].Lines > files[j].Lines
	})
}

func hygieneLargeFileAllowlist(repoDir string) map[string]bool {
	body, err := os.ReadFile(filepath.Join(repoDir, "docs", "architecture.md"))
	if err != nil {
		return nil
	}
	allowed := map[string]bool{}
	inSection := false
	for _, line := range strings.Split(string(body), "\n") {
		switch {
		case strings.TrimSpace(line) == "## File Size Budget Exceptions":
			inSection = true
			continue
		case inSection && strings.HasPrefix(line, "## "):
			return allowed
		case !inSection:
			continue
		}
		for {
			start := strings.Index(line, "`")
			if start < 0 {
				break
			}
			line = line[start+1:]
			end := strings.Index(line, "`")
			if end < 0 {
				break
			}
			path := line[:end]
			if strings.HasSuffix(path, ".go") {
				allowed[path] = true
			}
			line = line[end+1:]
		}
	}
	return allowed
}

func collectHygieneRuntimeArtifacts(repoDir string) []hygieneRuntimeArtifact {
	artifacts := make([]hygieneRuntimeArtifact, 0, len(hygieneRuntimeArtifactPaths))
	for _, path := range hygieneRuntimeArtifactPaths {
		size, exists, err := pathSize(filepath.Join(repoDir, filepath.FromSlash(path)))
		artifact := hygieneRuntimeArtifact{
			Path:   path,
			Exists: exists,
			Bytes:  size,
		}
		if err != nil {
			artifact.Error = err.Error()
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts
}

func hygieneLineLimit(path string) (int, string) {
	if strings.HasSuffix(path, "_test.go") {
		return hygieneGoTestFileLineLimit, "test"
	}
	return hygieneGoFileLineLimit, "source"
}

func countLines(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	lines := strings.Count(string(body), "\n")
	if body[len(body)-1] != '\n' {
		lines++
	}
	return lines
}

func generatedGoFile(body []byte) bool {
	prefix := string(body)
	if len(prefix) > 2048 {
		prefix = prefix[:2048]
	}
	return strings.Contains(prefix, "Code generated") && strings.Contains(prefix, "DO NOT EDIT")
}

func pathSize(path string) (int64, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if !info.IsDir() {
		return info.Size(), true, nil
	}
	var total int64
	err = filepath.WalkDir(path, func(child string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, true, err
}

func printHygieneReport(w io.Writer, report hygieneReport) {
	fmt.Fprintln(w, "billyharness hygiene")
	fmt.Fprintf(w, "repo: %s\n", report.RepoDir)
	fmt.Fprintf(w, "tracked Go files: %d\n", report.Source.TrackedGoFiles)
	if len(report.Source.LargeFiles) == 0 {
		fmt.Fprintln(w, "large source files: none")
	} else {
		fmt.Fprintln(w, "large source files:")
		for _, file := range report.Source.LargeFiles {
			fmt.Fprintf(w, "  %s: %d LOC > %d (%s)\n", file.Path, file.Lines, file.Limit, file.Kind)
		}
	}
	if len(report.Source.MissingFiles) > 0 {
		fmt.Fprintln(w, "missing tracked Go files:")
		for _, path := range report.Source.MissingFiles {
			fmt.Fprintf(w, "  %s\n", path)
		}
	}
	if len(report.Source.AllowedLargeFiles) > 0 {
		fmt.Fprintln(w, "allowed large source files:")
		for _, file := range report.Source.AllowedLargeFiles {
			fmt.Fprintf(w, "  %s: %d LOC > %d (%s)\n", file.Path, file.Lines, file.Limit, file.Kind)
		}
	}
	fmt.Fprintln(w, "runtime artifacts:")
	for _, artifact := range report.RuntimeArtifacts {
		if !artifact.Exists {
			fmt.Fprintf(w, "  %s: missing\n", artifact.Path)
			continue
		}
		if artifact.Error != "" {
			fmt.Fprintf(w, "  %s: error: %s\n", artifact.Path, artifact.Error)
			continue
		}
		fmt.Fprintf(w, "  %s: %s\n", artifact.Path, humanBytes(artifact.Bytes))
	}
}

func humanBytes(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= float64(unit)
		if value < float64(unit) {
			return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0") + " " + suffix
		}
	}
	return fmt.Sprintf("%.1f PiB", value/float64(unit))
}
