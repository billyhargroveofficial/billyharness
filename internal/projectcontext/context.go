package projectcontext

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/instructions"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	startMarker       = "# Project context"
	updateMarker      = "# Project context updated"
	openMarker        = "<PROJECT_CONTEXT>"
	endMarker         = "</PROJECT_CONTEXT>"
	defaultMaxBytes   = 4 * 1024
	maxWorkspaceRoots = 4
	maxEnvFiles       = 4
	maxEnvVars        = 32
	maxCommands       = 8
	maxSources        = 8
)

type Snapshot struct {
	CWD                string
	WorkspaceRoots     []string
	GitRoot            string
	PackageManagers    []PackageManager
	Commands           []LikelyCommand
	InstructionSources []InstructionSource
	EnvFiles           []EnvFile
	Caps               CapFlags
}

type PackageManager struct {
	Name       string
	Source     string
	Confidence string
}

type LikelyCommand struct {
	Name       string
	Command    string
	Source     string
	Confidence string
}

type InstructionSource struct {
	Path   string
	Scope  string
	Bytes  int
	SHA256 string
	Capped bool
}

type EnvFile struct {
	Path   string
	Vars   []string
	Capped bool
}

type CapFlags struct {
	MaxBytes             int
	WorkspaceRootsCapped bool
	CommandsCapped       bool
	InstructionCapped    bool
	EnvFilesCapped       bool
	EnvVarsCapped        bool
	RenderedCapped       bool
}

func Message(settings config.InstructionSettings) (protocol.Message, bool) {
	if settings.ProjectContextMaxBytes <= 0 {
		return protocol.Message{}, false
	}
	snapshot := SnapshotFromSettings(settings)
	content, ok := Render(snapshot, settings.ProjectContextMaxBytes)
	if !ok {
		return protocol.Message{}, false
	}
	return protocol.Message{Role: protocol.RoleUser, Content: content}, true
}

func ReconcileMessages(settings config.InstructionSettings, messages []protocol.Message) ([]protocol.Message, bool) {
	if settings.ProjectContextMaxBytes <= 0 {
		return messages, false
	}
	index := lastProjectContextIndex(messages)
	if index < 0 {
		return messages, false
	}
	snapshot := SnapshotFromSettings(settings)
	current, ok := Render(snapshot, settings.ProjectContextMaxBytes)
	if !ok {
		return messages, false
	}
	currentHash := ContentHash(current)
	if currentHash == "" || currentHash == ContentHash(messages[index].Content) {
		return messages, false
	}
	updated, ok := render(snapshot, settings.ProjectContextMaxBytes, updateMarker)
	if !ok {
		return messages, false
	}
	next := make([]protocol.Message, len(messages))
	copy(next, messages)
	next[index] = protocol.Message{Role: protocol.RoleUser, Content: updated}
	return next, true
}

func ContentHash(content string) string {
	body := projectContextBody(content)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func IsMessage(msg protocol.Message) bool {
	content := strings.TrimSpace(msg.Content)
	return msg.Role == protocol.RoleUser && strings.HasPrefix(content, startMarker) && ContentHash(content) != ""
}

func lastProjectContextIndex(messages []protocol.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if IsMessage(messages[i]) {
			return i
		}
	}
	return -1
}

func projectContextBody(content string) string {
	start := strings.Index(content, openMarker)
	end := strings.Index(content, endMarker)
	if start < 0 || end < start {
		return ""
	}
	end += len(endMarker)
	return strings.TrimSpace(content[start:end])
}

func SnapshotFromSettings(settings config.InstructionSettings) Snapshot {
	cwd := contextCWD(settings.WorkspaceRoots)
	root := findGitRoot(cwd)
	scanRoot := root
	if scanRoot == "" {
		scanRoot = cwd
	}
	roots, rootsCapped := workspaceRoots(settings.WorkspaceRoots, cwd)
	pms, commands, commandsCapped := detectProjectCommands(scanRoot)
	loaded := instructions.Load(settings)
	sources, instructionCapped := instructionSources(loaded.Sources)
	envFiles, envFilesCapped, envVarsCapped := envHints(scanRoot)
	return Snapshot{
		CWD:                cwd,
		WorkspaceRoots:     roots,
		GitRoot:            root,
		PackageManagers:    pms,
		Commands:           commands,
		InstructionSources: sources,
		EnvFiles:           envFiles,
		Caps: CapFlags{
			WorkspaceRootsCapped: rootsCapped,
			CommandsCapped:       commandsCapped,
			InstructionCapped:    instructionCapped,
			EnvFilesCapped:       envFilesCapped,
			EnvVarsCapped:        envVarsCapped,
		},
	}
}

func Render(snapshot Snapshot, maxBytes int) (string, bool) {
	return render(snapshot, maxBytes, startMarker)
}

func render(snapshot Snapshot, maxBytes int, marker string) (string, bool) {
	if strings.TrimSpace(snapshot.CWD) == "" {
		return "", false
	}
	if strings.TrimSpace(marker) == "" {
		marker = startMarker
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	if maxBytes == 0 {
		maxBytes = defaultMaxBytes
	}
	snapshot.Caps.MaxBytes = maxBytes
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", marker, openMarker)
	writeLine(&b, "cwd", snapshot.CWD)
	if len(snapshot.WorkspaceRoots) > 0 {
		b.WriteString("workspace_roots:\n")
		for _, root := range snapshot.WorkspaceRoots {
			fmt.Fprintf(&b, "- %s\n", escape(root))
		}
	}
	if snapshot.GitRoot != "" {
		writeLine(&b, "git_root", snapshot.GitRoot)
	}
	if len(snapshot.PackageManagers) > 0 {
		b.WriteString("package_managers:\n")
		for _, pm := range snapshot.PackageManagers {
			fmt.Fprintf(&b, "- %s source=%s confidence=%s\n", escape(pm.Name), escape(pm.Source), escape(pm.Confidence))
		}
	}
	if len(snapshot.Commands) > 0 {
		b.WriteString("likely_commands:\n")
		for _, cmd := range snapshot.Commands {
			fmt.Fprintf(&b, "- %s: %s source=%s confidence=%s\n", escape(cmd.Name), escape(cmd.Command), escape(cmd.Source), escape(cmd.Confidence))
		}
	}
	if len(snapshot.InstructionSources) > 0 {
		b.WriteString("instruction_sources:\n")
		for _, source := range snapshot.InstructionSources {
			line := fmt.Sprintf("- %s scope=%s bytes=%d sha256=%s", escape(source.Path), escape(source.Scope), source.Bytes, shortHash(source.SHA256))
			if source.Capped {
				line += " capped=true"
			}
			b.WriteString(line + "\n")
		}
	}
	if len(snapshot.EnvFiles) > 0 {
		b.WriteString("env_hints:\n")
		for _, env := range snapshot.EnvFiles {
			vars := strings.Join(escapedList(env.Vars), ",")
			line := fmt.Sprintf("- %s vars=%s", escape(env.Path), vars)
			if env.Capped {
				line += " capped=true"
			}
			b.WriteString(line + "\n")
		}
	}
	if caps := capFlagNames(snapshot.Caps); len(caps) > 0 {
		b.WriteString("cap_flags: " + strings.Join(caps, ",") + "\n")
	}
	b.WriteString(endMarker)
	content := b.String()
	if len(content) > maxBytes && !snapshot.Caps.RenderedCapped {
		snapshot.Caps.RenderedCapped = true
		return render(snapshot, maxBytes, marker)
	}
	if len(content) <= maxBytes {
		return content, true
	}
	suffix := "\n[project context truncated]\n" + endMarker
	limit := maxBytes - len(suffix)
	if limit < len(marker)+len(openMarker)+8 {
		limit = maxBytes
		suffix = ""
	}
	content = trimUTF8(content, limit) + suffix
	return content, true
}

func writeLine(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", key, escape(value))
}

func contextCWD(roots []string) string {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(root)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(cwd)
}

func workspaceRoots(in []string, fallback string) ([]string, bool) {
	var out []string
	seen := map[string]struct{}{}
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	for _, root := range in {
		add(root)
	}
	if len(out) == 0 {
		add(fallback)
	}
	capped := len(out) > maxWorkspaceRoots
	if capped {
		out = out[:maxWorkspaceRoots]
	}
	return out, capped
}

func findGitRoot(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	cursor := filepath.Clean(cwd)
	for {
		if exists(filepath.Join(cursor, ".git")) {
			return cursor
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return ""
		}
		cursor = parent
	}
}

func detectProjectCommands(root string) ([]PackageManager, []LikelyCommand, bool) {
	var managers []PackageManager
	var commands []LikelyCommand
	addManager := func(name, source, confidence string) {
		managers = append(managers, PackageManager{Name: name, Source: rel(root, filepath.Join(root, source)), Confidence: confidence})
	}
	addCommand := func(name, command, source, confidence string) {
		commands = append(commands, LikelyCommand{Name: name, Command: command, Source: rel(root, filepath.Join(root, source)), Confidence: confidence})
	}
	if exists(filepath.Join(root, "go.mod")) {
		addManager("go", "go.mod", "high")
		addCommand("test", "go test ./...", "go.mod", "high")
	}
	if exists(filepath.Join(root, "Cargo.toml")) {
		addManager("cargo", "Cargo.toml", "high")
		addCommand("test", "cargo test", "Cargo.toml", "high")
		addCommand("build", "cargo build", "Cargo.toml", "high")
	}
	if exists(filepath.Join(root, "package.json")) {
		pm, source := nodePackageManager(root)
		addManager(pm, source, "high")
		for _, script := range packageScripts(filepath.Join(root, "package.json")) {
			switch script {
			case "test":
				addCommand("test", nodeRunCommand(pm, "test"), "package.json", "high")
			case "build":
				addCommand("build", nodeRunCommand(pm, "build"), "package.json", "high")
			}
		}
	}
	if exists(filepath.Join(root, "pyproject.toml")) {
		addManager("python", "pyproject.toml", "medium")
		addCommand("test", "python -m pytest", "pyproject.toml", "low")
	}
	if exists(filepath.Join(root, "Makefile")) {
		addManager("make", "Makefile", "medium")
		targets := makeTargets(filepath.Join(root, "Makefile"))
		if targets["test"] {
			addCommand("test", "make test", "Makefile", "high")
		}
		if targets["build"] {
			addCommand("build", "make build", "Makefile", "high")
		}
	}
	commands = uniqueCommands(commands)
	capped := len(commands) > maxCommands
	if capped {
		commands = commands[:maxCommands]
	}
	return managers, commands, capped
}

func nodePackageManager(root string) (string, string) {
	switch {
	case exists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm", "pnpm-lock.yaml"
	case exists(filepath.Join(root, "yarn.lock")):
		return "yarn", "yarn.lock"
	case exists(filepath.Join(root, "bun.lock")):
		return "bun", "bun.lock"
	case exists(filepath.Join(root, "bun.lockb")):
		return "bun", "bun.lockb"
	case exists(filepath.Join(root, "package-lock.json")):
		return "npm", "package-lock.json"
	default:
		return "npm", "package.json"
	}
}

func nodeRunCommand(pm, script string) string {
	if pm == "npm" && script == "test" {
		return "npm test"
	}
	return pm + " run " + script
}

func packageScripts(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var parsed struct {
		Scripts map[string]any `json:"scripts"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return nil
	}
	var names []string
	for name := range parsed.Scripts {
		if name == "test" || name == "build" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func makeTargets(path string) map[string]bool {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	targets := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if ok {
			name = strings.TrimSpace(name)
			if name == "test" || name == "build" {
				targets[name] = true
			}
		}
	}
	return targets
}

func uniqueCommands(in []LikelyCommand) []LikelyCommand {
	seen := map[string]struct{}{}
	out := make([]LikelyCommand, 0, len(in))
	for _, command := range in {
		key := command.Name + "\x00" + command.Command
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, command)
	}
	return out
}

func instructionSources(in []instructions.Source) ([]InstructionSource, bool) {
	out := make([]InstructionSource, 0, len(in))
	for _, source := range in {
		if strings.TrimSpace(source.Path) == "" {
			continue
		}
		out = append(out, InstructionSource{
			Path:   source.Path,
			Scope:  source.Scope,
			Bytes:  source.Bytes,
			SHA256: source.SHA256,
			Capped: source.Capped,
		})
	}
	capped := len(out) > maxSources
	if capped {
		out = out[:maxSources]
	}
	return out, capped
}

func envHints(root string) ([]EnvFile, bool, bool) {
	names := []string{".env.example", ".env.sample", ".env.template", ".env.local.example", ".env"}
	var files []EnvFile
	var varsCapped bool
	for _, name := range names {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		vars, capped := parseEnvNames(path)
		if len(vars) == 0 {
			continue
		}
		varsCapped = varsCapped || capped
		files = append(files, EnvFile{Path: rel(root, path), Vars: vars, Capped: capped})
	}
	filesCapped := len(files) > maxEnvFiles
	if filesCapped {
		files = files[:maxEnvFiles]
	}
	return files, filesCapped, varsCapped
}

func parseEnvNames(path string) ([]string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	seen := map[string]struct{}{}
	var out []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !validEnvName(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
		if len(out) == maxEnvVars {
			return out, true
		}
	}
	sort.Strings(out)
	return out, false
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9' && i > 0) {
			continue
		}
		return false
	}
	return true
}

func capFlagNames(caps CapFlags) []string {
	var out []string
	if caps.WorkspaceRootsCapped {
		out = append(out, "workspace_roots_capped")
	}
	if caps.CommandsCapped {
		out = append(out, "commands_capped")
	}
	if caps.InstructionCapped {
		out = append(out, "instruction_sources_capped")
	}
	if caps.EnvFilesCapped {
		out = append(out, "env_files_capped")
	}
	if caps.EnvVarsCapped {
		out = append(out, "env_vars_capped")
	}
	if caps.RenderedCapped {
		out = append(out, "rendered_capped")
	}
	return out
}

func escape(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
}

func escapedList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, escape(value))
	}
	return out
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func trimUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return strings.TrimRight(value, "\n ")
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil && r != "." && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.Clean(path)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
