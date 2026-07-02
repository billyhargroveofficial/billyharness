package skills

import (
	"crypto/sha256"
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

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

const (
	SourceHome           = "home"
	SourceProject        = "project"
	SourceClaudeCompat   = "claude_compat"
	SourceHermesHome     = "hermes_home"
	SourceHermesRuntime  = "hermes_runtime"
	SourceHermesOptional = "hermes_optional"

	DefaultReadChars = 12 * 1024
	MaxReadChars     = 60 * 1024
	DefaultListLimit = 40
	MaxListLimit     = 200

	defaultHermesHomeSkillsDir     = "/root/.hermes/skills"
	defaultHermesRuntimeSkillsDir  = "/opt/hermes-agent-src/skills"
	defaultHermesOptionalSkillsDir = "/opt/hermes-agent-src/optional-skills"
)

var supportFileRoots = map[string]bool{
	"references": true,
	"templates":  true,
	"scripts":    true,
	"assets":     true,
}

type Options struct {
	WorkspaceRoots []string
	IncludeCompat  bool

	HomeDir                 string
	HermesHomeSkillsDir     string
	HermesRuntimeSkillsDir  string
	HermesOptionalSkillsDir string
	DisableDefaultHermes    bool
}

type Skill struct {
	Name         string   `json:"name"`
	Source       string   `json:"source"`
	Category     string   `json:"category,omitempty"`
	Path         string   `json:"path"`
	Dir          string   `json:"-"`
	Description  string   `json:"description,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	SupportFiles []string `json:"support_files,omitempty"`
}

type ListRequest struct {
	Query  string
	Source string
	Limit  int
}

type ListResult struct {
	Items      []Skill `json:"skills"`
	Truncated  bool    `json:"truncated"`
	Discovered int     `json:"discovered"`
}

type ViewRequest struct {
	Name     string
	Source   string
	FilePath string
	MaxChars int
}

type ViewResult struct {
	Skill         Skill  `json:"skill"`
	Content       string `json:"content"`
	FilePath      string `json:"file_path,omitempty"`
	ContentBytes  int    `json:"content_bytes"`
	ReturnedChars int    `json:"returned_chars"`
	Truncated     bool   `json:"truncated"`
}

type ImportRequest struct {
	Name   string
	Source string
	Force  bool
}

type ImportResult struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	SourcePath  string `json:"source_path"`
	SHA256      string `json:"sha256"`
	FilesCopied int    `json:"files_copied"`
	BytesCopied int64  `json:"bytes_copied"`
}

type importMetadata struct {
	SchemaVersion int       `json:"schema_version"`
	ImportedAt    time.Time `json:"imported_at"`
	Name          string    `json:"name"`
	Source        string    `json:"source"`
	SourcePath    string    `json:"source_path"`
	SourceDir     string    `json:"source_dir"`
	SHA256        string    `json:"sha256"`
}

type sourceDir struct {
	Source string
	Path   string
}

type parsedSkill struct {
	Name        string
	Description string
	Tags        []string
	Body        string
}

func Discover(opts Options) ([]Skill, error) {
	var out []Skill
	seen := map[string]bool{}
	for _, dir := range sourceDirs(opts) {
		paths, err := skillFiles(dir.Path)
		if err != nil {
			continue
		}
		for _, path := range paths {
			skill, err := readSkill(dir, path)
			if err != nil {
				continue
			}
			key := skill.Source + "/" + skill.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, skill)
		}
	}
	return out, nil
}

func List(opts Options, req ListRequest) (ListResult, error) {
	discovered, err := Discover(opts)
	if err != nil {
		return ListResult{}, err
	}
	limit := req.Limit
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	source := NormalizeSource(req.Source)
	var items []Skill
	truncated := false
	for _, skill := range discovered {
		if source != "" && skill.Source != source {
			continue
		}
		haystack := strings.ToLower(strings.Join([]string{
			skill.Name,
			skill.Source,
			skill.Category,
			skill.Description,
			skill.Summary,
			strings.Join(skill.Tags, " "),
		}, " "))
		if !matches(haystack, query) {
			continue
		}
		if len(items) >= limit {
			truncated = true
			break
		}
		items = append(items, skill)
	}
	return ListResult{Items: items, Truncated: truncated, Discovered: len(discovered)}, nil
}

func View(opts Options, req ViewRequest) (ViewResult, error) {
	name := NormalizeName(req.Name)
	if name == "" {
		return ViewResult{}, fmt.Errorf("name required")
	}
	source := NormalizeSource(req.Source)
	maxChars := NormalizeReadChars(req.MaxChars)
	for _, skill := range mustDiscover(opts) {
		if skill.Name != name {
			continue
		}
		if source != "" && skill.Source != source {
			continue
		}
		path := skill.Path
		if strings.TrimSpace(req.FilePath) != "" {
			resolved, err := resolveSupportFile(skill, req.FilePath)
			if err != nil {
				return ViewResult{}, err
			}
			path = resolved
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return ViewResult{}, err
		}
		content, truncated := truncateRunesWithMarker(string(body), maxChars)
		return ViewResult{
			Skill:         skill,
			Content:       content,
			FilePath:      supportFileLabel(skill, path),
			ContentBytes:  len(body),
			ReturnedChars: len([]rune(content)),
			Truncated:     truncated,
		}, nil
	}
	return ViewResult{}, fmt.Errorf("skill %q not found", req.Name)
}

func Import(opts Options, req ImportRequest) (ImportResult, error) {
	name := NormalizeName(req.Name)
	if name == "" {
		return ImportResult{}, fmt.Errorf("name required")
	}
	source := NormalizeSource(req.Source)
	if source == SourceHome {
		return ImportResult{}, fmt.Errorf("skill %q is already a home skill", name)
	}
	importOpts := opts
	importOpts.IncludeCompat = true
	for _, skill := range mustDiscover(importOpts) {
		if skill.Name != name {
			continue
		}
		if source != "" && skill.Source != source {
			continue
		}
		if skill.Source == SourceHome {
			continue
		}
		return importSkill(opts, skill, req.Force)
	}
	return ImportResult{}, fmt.Errorf("skill %q not found in importable sources", req.Name)
}

func NormalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return strings.Trim(value, ".-")
}

func NormalizeSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "":
		return ""
	case "compat", "claude", "claude_skills":
		return SourceClaudeCompat
	case "hermes", "hermes_active":
		return SourceHermesHome
	case "runtime", "hermes_skills":
		return SourceHermesRuntime
	case "optional", "hermes_optional_skills":
		return SourceHermesOptional
	default:
		return value
	}
}

func NormalizeReadChars(value int) int {
	if value <= 0 {
		return DefaultReadChars
	}
	if value > MaxReadChars {
		return MaxReadChars
	}
	return value
}

func sourceDirs(opts Options) []sourceDir {
	home := strings.TrimSpace(opts.HomeDir)
	if home == "" {
		home = config.BillyHomeDir()
	}
	dirs := []sourceDir{{Source: SourceHome, Path: filepath.Join(home, "skills")}}
	for _, root := range opts.WorkspaceRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		dirs = append(dirs, sourceDir{Source: SourceProject, Path: filepath.Join(root, ".billyharness", "skills")})
		if opts.IncludeCompat {
			dirs = append(dirs, sourceDir{Source: SourceClaudeCompat, Path: filepath.Join(root, ".claude", "skills")})
		}
	}
	if opts.IncludeCompat {
		for _, dir := range []sourceDir{
			{Source: SourceHermesHome, Path: defaultPath(opts.HermesHomeSkillsDir, defaultHermesHomeSkillsDir, opts.DisableDefaultHermes)},
			{Source: SourceHermesRuntime, Path: defaultPath(opts.HermesRuntimeSkillsDir, defaultHermesRuntimeSkillsDir, opts.DisableDefaultHermes)},
			{Source: SourceHermesOptional, Path: defaultPath(opts.HermesOptionalSkillsDir, defaultHermesOptionalSkillsDir, opts.DisableDefaultHermes)},
		} {
			if strings.TrimSpace(dir.Path) != "" {
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs
}

func defaultPath(value, fallback string, disable bool) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	if disable {
		return ""
	}
	return fallback
}

func skillFiles(root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, os.ErrNotExist
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != root && entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func readSkill(dir sourceDir, path string) (Skill, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	parsed := parseSkill(body)
	name := NormalizeName(parsed.Name)
	if name == "" {
		name = NormalizeName(filepath.Base(filepath.Dir(path)))
	}
	if name == "" {
		return Skill{}, errors.New("skill name empty")
	}
	relDir, _ := filepath.Rel(dir.Path, filepath.Dir(path))
	category := ""
	if relDir != "." && relDir != "" {
		parent := filepath.Dir(relDir)
		if parent != "." {
			category = filepath.ToSlash(parent)
		}
	}
	summary := parsed.Description
	if summary == "" {
		summary = firstBodySummary(parsed.Body)
	}
	return Skill{
		Name:         name,
		Source:       dir.Source,
		Category:     category,
		Path:         path,
		Dir:          filepath.Dir(path),
		Description:  parsed.Description,
		Summary:      truncate(oneLine(summary), 240),
		Tags:         parsed.Tags,
		SupportFiles: discoverSupportFiles(filepath.Dir(path)),
	}, nil
}

func parseSkill(body []byte) parsedSkill {
	text := string(body)
	parsed := parsedSkill{Body: text}
	if !strings.HasPrefix(text, "---") {
		return parsed
	}
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return parsed
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return parsed
	}
	parseFrontmatter(lines[1:end], &parsed)
	parsed.Body = strings.Join(lines[end+1:], "\n")
	return parsed
}

func parseFrontmatter(lines []string, parsed *parsedSkill) {
	section := ""
	subsection := ""
	tagListActive := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") && tagListActive {
			parsed.Tags = appendTags(parsed.Tags, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			tagListActive = key == "tags"
			if indent == 0 {
				section = key
				subsection = ""
			} else if section == "metadata" {
				subsection = key
			}
			continue
		}
		tagListActive = false
		switch key {
		case "name":
			if indent == 0 {
				parsed.Name = unquoteYAMLScalar(value)
			}
		case "description":
			if indent == 0 {
				parsed.Description = unquoteYAMLScalar(value)
			}
		case "tags":
			if indent == 0 || section == "metadata" || subsection == "hermes" {
				parsed.Tags = appendTags(parsed.Tags, value)
			}
		}
	}
	parsed.Tags = uniqueStrings(parsed.Tags)
}

func appendTags(tags []string, value string) []string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "[]")
	for _, part := range strings.Split(value, ",") {
		tag := strings.ToLower(strings.TrimSpace(unquoteYAMLScalar(part)))
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func unquoteYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
}

func firstBodySummary(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" && !strings.HasPrefix(line, "---") {
			return line
		}
	}
	return ""
}

func discoverSupportFiles(skillDir string) []string {
	var files []string
	for root := range supportFileRoots {
		dir := filepath.Join(skillDir, root)
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(skillDir, path)
			if err == nil {
				files = append(files, filepath.ToSlash(rel))
			}
			if len(files) >= 100 {
				return filepath.SkipAll
			}
			return nil
		})
	}
	sort.Strings(files)
	return files
}

func resolveSupportFile(skill Skill, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("file_path required")
	}
	if filepath.IsAbs(requested) {
		return "", fmt.Errorf("file_path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(requested))
	if clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", fmt.Errorf("file_path escapes skill directory")
	}
	first := strings.Split(filepath.ToSlash(clean), "/")[0]
	if !supportFileRoots[first] {
		return "", fmt.Errorf("file_path must be under references/, templates/, scripts/, or assets/")
	}
	target := filepath.Join(skill.Dir, clean)
	rel, err := filepath.Rel(skill.Dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("file_path escapes skill directory")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("support file is a symlink")
	}
	if info.IsDir() {
		return "", fmt.Errorf("support file is a directory")
	}
	return target, nil
}

func supportFileLabel(skill Skill, path string) string {
	if path == skill.Path {
		return ""
	}
	rel, err := filepath.Rel(skill.Dir, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func importSkill(opts Options, skill Skill, force bool) (ImportResult, error) {
	home := strings.TrimSpace(opts.HomeDir)
	if home == "" {
		home = config.BillyHomeDir()
	}
	destDir := filepath.Join(home, "skills", skill.Name)
	if _, err := os.Lstat(destDir); err == nil && !force {
		return ImportResult{}, fmt.Errorf("destination skill %q already exists", skill.Name)
	}
	if err := os.RemoveAll(destDir); err != nil {
		return ImportResult{}, err
	}
	files, bytesCopied, err := copyDir(skill.Dir, destDir)
	if err != nil {
		return ImportResult{}, err
	}
	sha, err := fileSHA256(skill.Path)
	if err != nil {
		return ImportResult{}, err
	}
	meta := importMetadata{
		SchemaVersion: 1,
		ImportedAt:    time.Now().UTC(),
		Name:          skill.Name,
		Source:        skill.Source,
		SourcePath:    skill.Path,
		SourceDir:     skill.Dir,
		SHA256:        sha,
	}
	metaBody, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return ImportResult{}, err
	}
	if err := os.WriteFile(filepath.Join(destDir, "billyharness.skill.json"), append(metaBody, '\n'), 0o600); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{
		Name:        skill.Name,
		Source:      skill.Source,
		Destination: destDir,
		SourcePath:  skill.Path,
		SHA256:      sha,
		FilesCopied: files,
		BytesCopied: bytesCopied,
	}, nil
}

func copyDir(src, dest string) (int, int64, error) {
	var files int
	var bytesCopied int64
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return err
		}
		files++
		bytesCopied += int64(len(data))
		return nil
	})
	return files, bytesCopied, err
}

func fileSHA256(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func mustDiscover(opts Options) []Skill {
	skills, _ := Discover(opts)
	return skills
}

func matches(haystack, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	if strings.Contains(haystack, query) {
		return true
	}
	for _, term := range strings.Fields(query) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func truncateRunesWithMarker(value string, maxChars int) (string, bool) {
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value, false
	}
	if maxChars <= 0 {
		return "...[truncated]", true
	}
	return string(runes[:maxChars]) + "...[truncated]", true
}

func truncate(value string, maxChars int) string {
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return string(runes[:maxChars])
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
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
