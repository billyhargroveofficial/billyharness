package checkpoint

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion = 1

	ChangeAdded    = "added"
	ChangeModified = "modified"
	ChangeDeleted  = "deleted"

	KindFile = "file"
	KindDir  = "dir"
)

var ErrConflict = errors.New("checkpoint restore conflict")

type Options struct {
	WorkspaceRoots  []string
	BaseDir         string
	MaxScanEntries  int
	MaxFileBytes    int64
	MaxPreviewBytes int
}

type Tracker struct {
	opts    Options
	tool    string
	targets []target
	before  map[string]FileState
}

type target struct {
	Path      string
	Recursive bool
}

type PatchRecord struct {
	SchemaVersion int         `json:"schema_version"`
	ChangeID      string      `json:"change_id"`
	CreatedAt     string      `json:"created_at"`
	ToolName      string      `json:"tool_name,omitempty"`
	TurnID        string      `json:"turn_id,omitempty"`
	StepID        string      `json:"step_id,omitempty"`
	CallID        string      `json:"call_id,omitempty"`
	AttemptID     string      `json:"attempt_id,omitempty"`
	Stats         ChangeStats `json:"stats"`
	Files         []FilePatch `json:"files,omitempty"`
	Truncated     bool        `json:"truncated,omitempty"`
}

type ChangeStats struct {
	FileCount   int `json:"file_count"`
	Added       int `json:"added"`
	Modified    int `json:"modified"`
	Deleted     int `json:"deleted"`
	Directories int `json:"directories,omitempty"`
	BinaryFiles int `json:"binary_files,omitempty"`
	LargeFiles  int `json:"large_files,omitempty"`
	Additions   int `json:"additions,omitempty"`
	Deletions   int `json:"deletions,omitempty"`
}

type FilePatch struct {
	Path       string     `json:"path"`
	RelPath    string     `json:"rel_path,omitempty"`
	Change     string     `json:"change"`
	Kind       string     `json:"kind,omitempty"`
	Before     *FileState `json:"before,omitempty"`
	After      *FileState `json:"after,omitempty"`
	Additions  int        `json:"additions,omitempty"`
	Deletions  int        `json:"deletions,omitempty"`
	Binary     bool       `json:"binary,omitempty"`
	Large      bool       `json:"large,omitempty"`
	Reversible bool       `json:"reversible"`
}

type FileState struct {
	Exists        bool   `json:"exists"`
	Kind          string `json:"kind,omitempty"`
	Mode          uint32 `json:"mode,omitempty"`
	Size          int64  `json:"size,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	Binary        bool   `json:"binary,omitempty"`
	Large         bool   `json:"large,omitempty"`
}

type RestoreResult struct {
	RestoredFiles []string `json:"restored_files,omitempty"`
	Conflicts     []string `json:"conflicts,omitempty"`
}

func DefaultOptions(roots []string) Options {
	return Options{
		WorkspaceRoots:  append([]string(nil), roots...),
		MaxScanEntries:  8000,
		MaxFileBytes:    256 * 1024,
		MaxPreviewBytes: 64 * 1024,
	}
}

func Begin(opts Options, toolName string, args json.RawMessage) (*Tracker, bool, error) {
	opts = normalizeOptions(opts)
	targets, tracked, err := targetsForTool(opts, toolName, args)
	if err != nil || !tracked {
		return nil, tracked, err
	}
	before, err := snapshotTargets(opts, targets)
	if err != nil {
		return nil, true, err
	}
	return &Tracker{opts: opts, tool: strings.TrimSpace(toolName), targets: targets, before: before}, true, nil
}

func (t *Tracker) Complete(turnID, stepID, callID, attemptID string) (PatchRecord, bool, error) {
	if t == nil {
		return PatchRecord{}, false, nil
	}
	after, err := snapshotTargets(t.opts, t.targets)
	if err != nil {
		return PatchRecord{}, false, err
	}
	record := diffSnapshots(t.opts, t.before, after)
	if len(record.Files) == 0 {
		return PatchRecord{}, false, nil
	}
	record.SchemaVersion = SchemaVersion
	record.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.ToolName = t.tool
	record.TurnID = turnID
	record.StepID = stepID
	record.CallID = callID
	record.AttemptID = attemptID
	record.ChangeID = changeID(record)
	return record, true, nil
}

func Load(path string) (PatchRecord, error) {
	bytes, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return PatchRecord{}, err
	}
	var record PatchRecord
	if err := json.Unmarshal(bytes, &record); err != nil {
		return PatchRecord{}, err
	}
	if record.SchemaVersion != 0 && record.SchemaVersion != SchemaVersion {
		return PatchRecord{}, fmt.Errorf("unsupported checkpoint schema_version %d", record.SchemaVersion)
	}
	return record, nil
}

func Preview(record PatchRecord, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	var b strings.Builder
	fmt.Fprintf(&b, "change %s via %s: %d files (+%d -%d)\n", record.ChangeID, record.ToolName, record.Stats.FileCount, record.Stats.Additions, record.Stats.Deletions)
	for _, file := range record.Files {
		fmt.Fprintf(&b, "\n%s %s\n", strings.ToUpper(file.Change[:1]), displayPath(file))
		if file.Kind == KindDir {
			b.WriteString("  directory\n")
		}
		if file.Binary || file.Large || !file.Reversible {
			var flags []string
			if file.Binary {
				flags = append(flags, "binary")
			}
			if file.Large {
				flags = append(flags, "large")
			}
			if !file.Reversible {
				flags = append(flags, "not reversible")
			}
			fmt.Fprintf(&b, "  %s\n", strings.Join(flags, ", "))
			if b.Len() > maxBytes {
				return b.String()[:maxBytes], true
			}
			continue
		}
		before := decodedText(file.Before)
		after := decodedText(file.After)
		writePreviewDiff(&b, displayPath(file), before, after)
		if b.Len() > maxBytes {
			return b.String()[:maxBytes], true
		}
	}
	return strings.TrimRight(b.String(), "\n"), false
}

func Restore(record PatchRecord) (RestoreResult, error) {
	return restoreRecord(record, false)
}

func Redo(record PatchRecord) (RestoreResult, error) {
	return restoreRecord(record, true)
}

func restoreRecord(record PatchRecord, useAfter bool) (RestoreResult, error) {
	conflicts := restoreConflicts(record, useAfter)
	if len(conflicts) > 0 {
		return RestoreResult{Conflicts: conflicts}, ErrConflict
	}
	files := append([]FilePatch(nil), record.Files...)
	sort.Slice(files, func(i, j int) bool {
		removeI := restoreRemoves(files[i], useAfter)
		removeJ := restoreRemoves(files[j], useAfter)
		if removeI != removeJ {
			return removeI
		}
		depthI := strings.Count(files[i].Path, string(os.PathSeparator))
		depthJ := strings.Count(files[j].Path, string(os.PathSeparator))
		if removeI {
			return depthI > depthJ
		}
		return depthI < depthJ
	})
	var restored []string
	for _, file := range files {
		if err := restoreOne(file, useAfter); err != nil {
			return RestoreResult{RestoredFiles: restored}, err
		}
		restored = append(restored, file.Path)
	}
	return RestoreResult{RestoredFiles: restored}, nil
}

func restoreRemoves(file FilePatch, useAfter bool) bool {
	target := restoreTargetState(file, useAfter)
	return !target.Exists
}

func normalizeOptions(opts Options) Options {
	if opts.MaxScanEntries <= 0 {
		opts.MaxScanEntries = 8000
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = 256 * 1024
	}
	if opts.MaxPreviewBytes <= 0 {
		opts.MaxPreviewBytes = 64 * 1024
	}
	if opts.BaseDir == "" {
		if len(opts.WorkspaceRoots) > 0 {
			opts.BaseDir = opts.WorkspaceRoots[0]
		} else {
			opts.BaseDir, _ = os.Getwd()
		}
	}
	return opts
}

func targetsForTool(opts Options, toolName string, args json.RawMessage) ([]target, bool, error) {
	switch toolName {
	case "fs_write_file", "fs_edit_file":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(normalizeArgs(args), &in); err != nil {
			return nil, true, err
		}
		path, err := resolvePath(opts, in.Path)
		return []target{{Path: path}}, true, err
	case "fs_make_dir":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(normalizeArgs(args), &in); err != nil {
			return nil, true, err
		}
		path, err := resolvePath(opts, in.Path)
		return []target{{Path: path}}, true, err
	case "shell_exec":
		var in struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal(normalizeArgs(args), &in); err != nil {
			return nil, true, err
		}
		if in.CWD == "" {
			in.CWD = "."
		}
		path, err := resolvePath(opts, in.CWD)
		return []target{{Path: path, Recursive: true}}, true, err
	default:
		return nil, false, nil
	}
}

func normalizeArgs(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || strings.TrimSpace(string(args)) == "" || strings.TrimSpace(string(args)) == "null" {
		return json.RawMessage(`{}`)
	}
	return args
}

func snapshotTargets(opts Options, targets []target) (map[string]FileState, error) {
	out := map[string]FileState{}
	remaining := opts.MaxScanEntries
	for _, target := range targets {
		path := filepath.Clean(target.Path)
		if !target.Recursive {
			state, err := snapshotPath(opts, path)
			if err != nil {
				return nil, err
			}
			out[path] = state
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				out[path] = FileState{}
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			state, err := snapshotPath(opts, path)
			if err != nil {
				return nil, err
			}
			out[path] = state
			continue
		}
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if remaining <= 0 {
				return filepath.SkipDir
			}
			if current != path && entry.IsDir() && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			if sensitive(current) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			state, err := snapshotPath(opts, current)
			if err != nil {
				return nil
			}
			out[filepath.Clean(current)] = state
			remaining--
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func snapshotPath(opts Options, path string) (FileState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileState{}, nil
		}
		return FileState{}, err
	}
	state := FileState{
		Exists: true,
		Mode:   uint32(info.Mode().Perm()),
		Size:   info.Size(),
	}
	if info.IsDir() {
		state.Kind = KindDir
		return state, nil
	}
	if !info.Mode().IsRegular() {
		return FileState{Exists: true, Kind: info.Mode().Type().String(), Mode: uint32(info.Mode().Perm()), Size: info.Size(), Large: true}, nil
	}
	state.Kind = KindFile
	bytes, err := os.ReadFile(path)
	if err != nil {
		return FileState{}, err
	}
	sum := sha256.Sum256(bytes)
	state.SHA256 = hex.EncodeToString(sum[:])
	state.Size = int64(len(bytes))
	state.Binary = isBinary(bytes)
	state.Large = int64(len(bytes)) > opts.MaxFileBytes
	if !state.Large {
		state.ContentBase64 = base64.StdEncoding.EncodeToString(bytes)
	}
	return state, nil
}

func diffSnapshots(opts Options, before, after map[string]FileState) PatchRecord {
	paths := map[string]struct{}{}
	for path := range before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	record := PatchRecord{}
	for _, path := range ordered {
		b := before[path]
		a := after[path]
		if statesEqual(b, a) {
			continue
		}
		patch := FilePatch{
			Path:       path,
			RelPath:    relPath(opts.WorkspaceRoots, path),
			Before:     statePtr(b),
			After:      statePtr(a),
			Kind:       patchKind(b, a),
			Binary:     b.Binary || a.Binary,
			Large:      b.Large || a.Large,
			Reversible: reversible(b, a),
		}
		switch {
		case !b.Exists && a.Exists:
			patch.Change = ChangeAdded
			record.Stats.Added++
		case b.Exists && !a.Exists:
			patch.Change = ChangeDeleted
			record.Stats.Deleted++
		default:
			patch.Change = ChangeModified
			record.Stats.Modified++
		}
		patch.Additions, patch.Deletions = lineStats(b, a)
		record.Stats.Additions += patch.Additions
		record.Stats.Deletions += patch.Deletions
		if patch.Kind == KindDir {
			record.Stats.Directories++
		}
		if patch.Binary {
			record.Stats.BinaryFiles++
		}
		if patch.Large {
			record.Stats.LargeFiles++
		}
		record.Files = append(record.Files, patch)
	}
	record.Stats.FileCount = len(record.Files)
	return record
}

func statesEqual(a, b FileState) bool {
	return a.Exists == b.Exists &&
		a.Kind == b.Kind &&
		a.Mode == b.Mode &&
		a.Size == b.Size &&
		a.SHA256 == b.SHA256
}

func statePtr(state FileState) *FileState {
	return &state
}

func patchKind(before, after FileState) string {
	if after.Exists {
		return after.Kind
	}
	return before.Kind
}

func reversible(before, after FileState) bool {
	if before.Exists && before.Kind == KindFile && before.ContentBase64 == "" {
		return false
	}
	if after.Exists && after.Kind == KindFile && after.SHA256 == "" {
		return false
	}
	return true
}

func lineStats(before, after FileState) (int, int) {
	if before.Binary || after.Binary || before.Large || after.Large || before.Kind == KindDir || after.Kind == KindDir {
		return 0, 0
	}
	beforeText := decodedText(&before)
	afterText := decodedText(&after)
	if beforeText == "" && afterText == "" {
		return 0, 0
	}
	if !before.Exists {
		return lineCount(afterText), 0
	}
	if !after.Exists {
		return 0, lineCount(beforeText)
	}
	return simpleLineDiff(beforeText, afterText)
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	return len(lines)
}

func simpleLineDiff(before, after string) (int, int) {
	a := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	b := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	if len(a) == 1 && a[0] == "" {
		a = nil
	}
	if len(b) == 1 && b[0] == "" {
		b = nil
	}
	if len(a)*len(b) > 40000 {
		if len(b) > len(a) {
			return len(b) - len(a), 0
		}
		return 0, len(a) - len(b)
	}
	prev := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] >= cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev = cur
	}
	common := prev[len(b)]
	return len(b) - common, len(a) - common
}

func restoreConflicts(record PatchRecord, useAfter bool) []string {
	recordPaths := map[string]struct{}{}
	for _, file := range record.Files {
		recordPaths[filepath.Clean(file.Path)] = struct{}{}
	}
	var conflicts []string
	for _, file := range record.Files {
		target := restoreTargetState(file, useAfter)
		expectedCurrent := restoreConflictState(file, useAfter)
		if !restorableTarget(target) {
			conflicts = append(conflicts, fmt.Sprintf("%s: patch is not reversible", displayPath(file)))
			continue
		}
		current, err := snapshotPath(DefaultOptions(nil), file.Path)
		if err != nil {
			conflicts = append(conflicts, fmt.Sprintf("%s: %v", displayPath(file), err))
			continue
		}
		if !statesEqualForConflict(current, expectedCurrent) {
			conflicts = append(conflicts, fmt.Sprintf("%s: current file changed after checkpoint", displayPath(file)))
			continue
		}
		if !target.Exists && expectedCurrent.Exists && expectedCurrent.Kind == KindDir {
			extra, err := extraDirEntries(file.Path, recordPaths)
			if err != nil {
				conflicts = append(conflicts, fmt.Sprintf("%s: %v", displayPath(file), err))
			} else if len(extra) > 0 {
				conflicts = append(conflicts, fmt.Sprintf("%s: directory contains files outside checkpoint", displayPath(file)))
			}
		}
	}
	return conflicts
}

func restoreTargetState(file FilePatch, useAfter bool) FileState {
	if useAfter {
		return derefState(file.After)
	}
	return derefState(file.Before)
}

func restoreConflictState(file FilePatch, useAfter bool) FileState {
	if useAfter {
		return derefState(file.Before)
	}
	return derefState(file.After)
}

func restorableTarget(target FileState) bool {
	if !target.Exists {
		return true
	}
	switch target.Kind {
	case KindDir:
		return true
	case KindFile:
		return target.ContentBase64 != ""
	default:
		return false
	}
}

func statesEqualForConflict(current, recorded FileState) bool {
	if current.Exists != recorded.Exists || current.Kind != recorded.Kind {
		return false
	}
	if !current.Exists {
		return true
	}
	if current.Kind == KindDir {
		return true
	}
	if recorded.SHA256 != "" {
		return current.SHA256 == recorded.SHA256
	}
	return current.Size == recorded.Size && current.Mode == recorded.Mode
}

func restoreOne(file FilePatch, useAfter bool) error {
	target := restoreTargetState(file, useAfter)
	if !target.Exists {
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	switch target.Kind {
	case KindDir:
		if err := os.MkdirAll(file.Path, fs.FileMode(target.Mode)); err != nil {
			return err
		}
		return os.Chmod(file.Path, fs.FileMode(target.Mode))
	case KindFile:
		bytes, err := base64.StdEncoding.DecodeString(target.ContentBase64)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file.Path, bytes, fs.FileMode(target.Mode)); err != nil {
			return err
		}
		return os.Chmod(file.Path, fs.FileMode(target.Mode))
	default:
		return fmt.Errorf("%s: unsupported restore kind %q", displayPath(file), target.Kind)
	}
}

func derefState(state *FileState) FileState {
	if state == nil {
		return FileState{}
	}
	return *state
}

func extraDirEntries(dir string, recordPaths map[string]struct{}) ([]string, error) {
	var extra []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return nil
		}
		if _, ok := recordPaths[filepath.Clean(path)]; !ok {
			extra = append(extra, path)
			if entry.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return extra, err
}

func writePreviewDiff(b *strings.Builder, path, before, after string) {
	fmt.Fprintf(b, "--- %s\n", path)
	fmt.Fprintf(b, "+++ %s\n", path)
	beforeLines := splitPreviewLines(before)
	afterLines := splitPreviewLines(after)
	max := len(beforeLines)
	if len(afterLines) > max {
		max = len(afterLines)
	}
	for i := 0; i < max; i++ {
		var oldLine, newLine string
		if i < len(beforeLines) {
			oldLine = beforeLines[i]
		}
		if i < len(afterLines) {
			newLine = afterLines[i]
		}
		if oldLine == newLine {
			fmt.Fprintf(b, " %s\n", oldLine)
			continue
		}
		if oldLine != "" {
			fmt.Fprintf(b, "-%s\n", oldLine)
		}
		if newLine != "" {
			fmt.Fprintf(b, "+%s\n", newLine)
		}
	}
}

func splitPreviewLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > 120 {
		lines = append(lines[:120], "...[preview truncated]")
	}
	return lines
}

func decodedText(state *FileState) string {
	if state == nil || !state.Exists || state.Kind != KindFile || state.Binary || state.Large || state.ContentBase64 == "" {
		return ""
	}
	bytes, err := base64.StdEncoding.DecodeString(state.ContentBase64)
	if err != nil {
		return ""
	}
	if !utf8.Valid(bytes) {
		return ""
	}
	return string(bytes)
}

func changeID(record PatchRecord) string {
	body, _ := json.Marshal(struct {
		CreatedAt string      `json:"created_at"`
		ToolName  string      `json:"tool_name"`
		TurnID    string      `json:"turn_id"`
		StepID    string      `json:"step_id"`
		CallID    string      `json:"call_id"`
		AttemptID string      `json:"attempt_id"`
		Files     []FilePatch `json:"files"`
	}{record.CreatedAt, record.ToolName, record.TurnID, record.StepID, record.CallID, record.AttemptID, record.Files})
	sum := sha256.Sum256(body)
	return "change-" + hex.EncodeToString(sum[:8])
}

func relPath(roots []string, path string) string {
	path = filepath.Clean(path)
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(absRoot, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
			return filepath.ToSlash(rel)
		}
	}
	return ""
}

func displayPath(file FilePatch) string {
	if file.RelPath != "" {
		return file.RelPath
	}
	return file.Path
}

func resolvePath(opts Options, input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		input = "."
	}
	if !filepath.IsAbs(input) {
		input = filepath.Join(opts.BaseDir, input)
	}
	path, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	if sensitive(path) {
		return "", fmt.Errorf("refusing sensitive path %s", path)
	}
	policyPath, err := resolvedPathForPolicy(path)
	if err != nil {
		return "", err
	}
	if sensitive(policyPath) {
		return "", fmt.Errorf("refusing sensitive path %s", policyPath)
	}
	for _, root := range opts.WorkspaceRoots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		absRoot, _ := filepath.Abs(root)
		policyRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			policyRoot = absRoot
		}
		policyRoot, _ = filepath.Abs(policyRoot)
		if policyPath == policyRoot || strings.HasPrefix(policyPath, policyRoot+string(os.PathSeparator)) {
			return filepath.Clean(path), nil
		}
	}
	return "", fmt.Errorf("path outside workspace roots: %s", path)
}

func resolvedPathForPolicy(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Abs(resolved)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	cursor := filepath.Clean(path)
	var missing []string
	for {
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return filepath.Abs(path)
		}
		missing = append([]string{filepath.Base(cursor)}, missing...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			for _, part := range missing {
				resolved = filepath.Join(resolved, part)
			}
			return filepath.Abs(resolved)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		cursor = parent
	}
}

func sensitive(path string) bool {
	lower := strings.ToLower(path)
	for _, needle := range []string{".env", ".ssh", "id_rsa", "id_ed25519", "auth.json", "token", "secret", ".aws", ".kube", ".docker"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func isBinary(bytes []byte) bool {
	if len(bytes) == 0 {
		return false
	}
	if !utf8.Valid(bytes) {
		return true
	}
	for _, b := range bytes {
		if b == 0 {
			return true
		}
	}
	return false
}
