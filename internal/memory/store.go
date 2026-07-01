package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	EntryPointName         = "MEMORY.md"
	DefaultSummaryMaxBytes = 2 * 1024
	DefaultIndexMaxBytes   = 25 * 1024
	DefaultTopicMaxBytes   = 64 * 1024
	MaxPromptEntries       = 32

	startMarker = "# Memory context"
	openMarker  = "<MEMORY_CONTEXT>"
	endMarker   = "</MEMORY_CONTEXT>"
)

type Root struct {
	Source    string
	Dir       string
	IndexPath string
}

type Entry struct {
	Type         string
	Topic        string
	Summary      string
	Path         string
	Source       string
	RootDir      string
	IndexPath    string
	IndexLine    int
	TopicBytes   int64
	TopicCapped  bool
	TopicMissing bool
	Blocked      bool
	BlockReason  string
}

type Snapshot struct {
	Roots    []Root
	Entries  []Entry
	Warnings []string
	Caps     CapFlags
}

type CapFlags struct {
	SummaryMaxBytes int
	IndexMaxBytes   int
	TopicMaxBytes   int
	IndexCapped     bool
	TopicCapped     bool
	EntriesCapped   bool
	RenderedCapped  bool
}

type caps struct {
	summary int
	index   int
	topic   int
}

func Message(settings config.InstructionSettings) (protocol.Message, bool) {
	if !settings.MemoryEnabled || settings.MemorySummaryMaxBytes <= 0 {
		return protocol.Message{}, false
	}
	snapshot, err := Load(settings)
	if err != nil || len(snapshot.Entries) == 0 {
		return protocol.Message{}, false
	}
	content, ok := Render(snapshot, settings.MemorySummaryMaxBytes)
	if !ok {
		return protocol.Message{}, false
	}
	return protocol.Message{Role: protocol.RoleUser, Content: content}, true
}

func Load(settings config.InstructionSettings) (Snapshot, error) {
	if !settings.MemoryEnabled {
		return Snapshot{}, nil
	}
	limit := limits(settings)
	snapshot := Snapshot{Caps: CapFlags{
		SummaryMaxBytes: limit.summary,
		IndexMaxBytes:   limit.index,
		TopicMaxBytes:   limit.topic,
	}}
	for _, root := range Roots(settings) {
		info, err := os.Stat(root.IndexPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return snapshot, err
		}
		if info.IsDir() {
			return snapshot, fmt.Errorf("memory index %s is a directory", root.IndexPath)
		}
		raw, capped, err := readCapped(root.IndexPath, limit.index)
		if err != nil {
			return snapshot, err
		}
		if capped {
			snapshot.Caps.IndexCapped = true
			snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf("%s exceeded index cap", root.IndexPath))
		}
		entries, warnings, err := parseIndex(root, raw, limit.topic)
		if err != nil {
			return snapshot, err
		}
		if len(entries) > 0 || capped {
			snapshot.Roots = append(snapshot.Roots, root)
		}
		for _, entry := range entries {
			snapshot.Caps.TopicCapped = snapshot.Caps.TopicCapped || entry.TopicCapped
			snapshot.Entries = append(snapshot.Entries, entry)
		}
		snapshot.Warnings = append(snapshot.Warnings, warnings...)
	}
	sort.SliceStable(snapshot.Entries, func(i, j int) bool {
		a, b := snapshot.Entries[i], snapshot.Entries[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Topic < b.Topic
	})
	return snapshot, nil
}

func Roots(settings config.InstructionSettings) []Root {
	var roots []Root
	add := func(source, dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "." || dir == "" {
			return
		}
		for _, existing := range roots {
			if existing.Dir == dir {
				return
			}
		}
		roots = append(roots, Root{
			Source:    source,
			Dir:       dir,
			IndexPath: filepath.Join(dir, EntryPointName),
		})
	}
	add("home", filepath.Join(config.BillyHomeDir(), "memory"))
	if profile := strings.TrimSpace(settings.Profile.Profile); profile != "" {
		add("profile:"+config.NormalizeProfileName(profile), filepath.Join(config.DefaultProfileDir(profile), "memory"))
	}
	return roots
}

func Render(snapshot Snapshot, maxBytes int) (string, bool) {
	if len(snapshot.Entries) == 0 {
		return "", false
	}
	if maxBytes <= 0 {
		maxBytes = DefaultSummaryMaxBytes
	}
	snapshot.Caps.SummaryMaxBytes = maxBytes
	return render(snapshot, maxBytes)
}

func render(snapshot Snapshot, maxBytes int) (string, bool) {
	entries := snapshot.Entries
	if len(entries) > MaxPromptEntries {
		entries = entries[:MaxPromptEntries]
		snapshot.Caps.EntriesCapped = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", startMarker, openMarker)
	b.WriteString("policy: summary_only; read topic files explicitly when exact details are needed\n")
	if len(snapshot.Roots) > 0 {
		b.WriteString("roots:\n")
		for _, root := range snapshot.Roots {
			fmt.Fprintf(&b, "- source=%s path=%s\n", quote(root.Source), quote(root.Dir))
		}
	}
	b.WriteString("entries:\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, "- type=%s topic=%s summary=%s path=%s source=%s", quote(entry.Type), quote(entry.Topic), quote(entry.Summary), quote(entry.Path), quote(entry.Source))
		if entry.TopicBytes > 0 {
			fmt.Fprintf(&b, " bytes=%d", entry.TopicBytes)
		}
		if entry.TopicCapped {
			b.WriteString(" capped=true")
		}
		if entry.TopicMissing {
			b.WriteString(" missing=true")
		}
		if entry.Blocked {
			fmt.Fprintf(&b, " blocked=%s", quote(entry.BlockReason))
		}
		b.WriteString("\n")
	}
	if len(snapshot.Warnings) > 0 {
		b.WriteString("warnings:\n")
		for _, warning := range snapshot.Warnings {
			fmt.Fprintf(&b, "- %s\n", quote(warning))
		}
	}
	if names := capFlagNames(snapshot.Caps); len(names) > 0 {
		b.WriteString("cap_flags: " + strings.Join(names, ",") + "\n")
	}
	b.WriteString(endMarker)
	content := b.String()
	if len([]byte(content)) > maxBytes && !snapshot.Caps.RenderedCapped {
		snapshot.Caps.RenderedCapped = true
		return render(snapshot, maxBytes)
	}
	if len([]byte(content)) <= maxBytes {
		return content, true
	}
	suffix := "\n[memory context truncated]\n" + endMarker
	limit := maxBytes - len([]byte(suffix))
	if limit < len(startMarker)+len(openMarker)+8 {
		return trimUTF8(content, maxBytes), true
	}
	return trimUTF8(content, limit) + suffix, true
}

func ContentHash(content string) string {
	body := memoryBody(content)
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

func parseIndex(root Root, raw []byte, topicMaxBytes int) ([]Entry, []string, error) {
	var entries []Entry
	var warnings []string
	for i, line := range strings.Split(string(raw), "\n") {
		entry, ok, err := parseIndexLine(line)
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", root.IndexPath, i+1, err)
		}
		if !ok {
			continue
		}
		cleanPath, absPath, err := validateEntryPath(root.Dir, entry.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", root.IndexPath, i+1, err)
		}
		entry.Path = cleanPath
		entry.Source = root.Source
		entry.RootDir = root.Dir
		entry.IndexPath = root.IndexPath
		entry.IndexLine = i + 1
		if summary, blocked, reason := sanitizeSummary(entry.Summary); blocked {
			entry.Summary = summary
			entry.Blocked = true
			entry.BlockReason = reason
			warnings = append(warnings, fmt.Sprintf("%s:%d blocked prompt-like summary for topic %q", root.IndexPath, i+1, entry.Topic))
		} else {
			entry.Summary = summary
		}
		if info, err := os.Stat(absPath); err == nil {
			if info.IsDir() {
				entry.TopicMissing = true
				warnings = append(warnings, fmt.Sprintf("%s:%d topic path is a directory: %s", root.IndexPath, i+1, entry.Path))
			} else {
				entry.TopicBytes = info.Size()
				entry.TopicCapped = topicMaxBytes > 0 && info.Size() > int64(topicMaxBytes)
			}
		} else if os.IsNotExist(err) {
			entry.TopicMissing = true
		} else {
			return nil, nil, err
		}
		entries = append(entries, entry)
	}
	return entries, warnings, nil
}

func parseIndexLine(line string) (Entry, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return Entry{}, false, nil
	}
	if !strings.HasPrefix(line, "-") {
		return Entry{}, false, nil
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
	fields, err := parseKeyValues(line)
	if err != nil {
		return Entry{}, false, err
	}
	entry := Entry{
		Type:    strings.TrimSpace(fields["type"]),
		Topic:   strings.TrimSpace(fields["topic"]),
		Summary: strings.TrimSpace(fields["summary"]),
		Path:    strings.TrimSpace(fields["path"]),
	}
	switch {
	case entry.Type == "":
		return Entry{}, false, fmt.Errorf("memory entry missing type")
	case entry.Topic == "":
		return Entry{}, false, fmt.Errorf("memory entry missing topic")
	case entry.Summary == "":
		return Entry{}, false, fmt.Errorf("memory entry missing summary")
	case entry.Path == "":
		return Entry{}, false, fmt.Errorf("memory entry missing path")
	default:
		return entry, true, nil
	}
}

func parseKeyValues(line string) (map[string]string, error) {
	out := map[string]string{}
	for i := 0; i < len(line); {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}
		keyStart := i
		for i < len(line) && line[i] != '=' && line[i] != ' ' {
			i++
		}
		if i >= len(line) || line[i] != '=' {
			return nil, fmt.Errorf("expected key=value field near %q", line[keyStart:])
		}
		key := strings.TrimSpace(line[keyStart:i])
		i++
		value, next, err := parseValue(line, i)
		if err != nil {
			return nil, err
		}
		if key != "" {
			out[key] = value
		}
		i = next
	}
	return out, nil
}

func parseValue(line string, start int) (string, int, error) {
	if start >= len(line) {
		return "", start, nil
	}
	if line[start] != '"' {
		i := start
		for i < len(line) && line[i] != ' ' {
			i++
		}
		return line[start:i], i, nil
	}
	var b strings.Builder
	i := start + 1
	for i < len(line) {
		ch := line[i]
		switch ch {
		case '\\':
			if i+1 >= len(line) {
				return "", i, fmt.Errorf("unterminated escape in quoted value")
			}
			b.WriteByte(line[i+1])
			i += 2
		case '"':
			return b.String(), i + 1, nil
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return "", i, fmt.Errorf("unterminated quoted value")
}

func validateEntryPath(rootDir, value string) (string, string, error) {
	if strings.ContainsRune(value, 0) {
		return "", "", fmt.Errorf("memory path contains NUL byte")
	}
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" {
		return "", "", fmt.Errorf("memory path is empty")
	}
	if strings.HasPrefix(value, "/") || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", "", fmt.Errorf("memory path %q must be relative to the memory root", value)
	}
	clean := pathpkg.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", fmt.Errorf("memory path %q escapes the memory root", value)
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", "", err
	}
	absPath := filepath.Join(absRoot, filepath.FromSlash(clean))
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "", fmt.Errorf("memory path %q escapes the memory root", value)
	}
	return clean, absPath, nil
}

func readCapped(path string, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultIndexMaxBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return nil, false, err
	}
	capped := len(raw) > maxBytes
	if capped {
		raw = raw[:maxBytes]
		raw = completeLines(raw)
	}
	return raw, capped, nil
}

func completeLines(raw []byte) []byte {
	if len(raw) == 0 || raw[len(raw)-1] == '\n' {
		return raw
	}
	idx := bytes.LastIndexByte(raw, '\n')
	if idx < 0 {
		return nil
	}
	return raw[:idx+1]
}

func limits(settings config.InstructionSettings) caps {
	out := caps{
		summary: settings.MemorySummaryMaxBytes,
		index:   settings.MemoryIndexMaxBytes,
		topic:   settings.MemoryTopicMaxBytes,
	}
	if out.summary <= 0 {
		out.summary = DefaultSummaryMaxBytes
	}
	if out.index <= 0 {
		out.index = DefaultIndexMaxBytes
	}
	if out.topic <= 0 {
		out.topic = DefaultTopicMaxBytes
	}
	return out
}

func sanitizeSummary(value string) (string, bool, string) {
	value = strings.Join(strings.Fields(value), " ")
	lower := strings.ToLower(value)
	for _, pattern := range []string{
		"<memory_context>",
		"</memory_context>",
		"ignore previous instructions",
		"disregard previous instructions",
	} {
		if strings.Contains(lower, pattern) {
			return "[blocked: prompt-like memory summary]", true, "prompt_like_summary"
		}
	}
	return value, false, ""
}

func memoryBody(content string) string {
	start := strings.Index(content, openMarker)
	end := strings.Index(content, endMarker)
	if start < 0 || end < start {
		return ""
	}
	end += len(endMarker)
	return strings.TrimSpace(content[start:end])
}

func capFlagNames(caps CapFlags) []string {
	var out []string
	if caps.IndexCapped {
		out = append(out, "index_capped")
	}
	if caps.TopicCapped {
		out = append(out, "topic_capped")
	}
	if caps.EntriesCapped {
		out = append(out, "entries_capped")
	}
	if caps.RenderedCapped {
		out = append(out, "rendered_capped")
	}
	return out
}

func quote(value string) string {
	return strconv.Quote(value)
}

func trimUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len([]byte(value)) <= maxBytes {
		return value
	}
	raw := []byte(value)
	if maxBytes > len(raw) {
		maxBytes = len(raw)
	}
	raw = raw[:maxBytes]
	for len(raw) > 0 && !utf8.Valid(raw) {
		raw = raw[:len(raw)-1]
	}
	return string(raw)
}
