package memory

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

const (
	DefaultListLimit      = 50
	DefaultSearchLimit    = 20
	DefaultReadMaxBytes   = 12 * 1024
	MaxReadMaxBytes       = 64 * 1024
	defaultMemoryFileMode = 0o600
	defaultMemoryDirMode  = 0o700
)

type OperationInput struct {
	Op       string `json:"op,omitempty"`
	Source   string `json:"source,omitempty"`
	Type     string `json:"type,omitempty"`
	Topic    string `json:"topic,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Path     string `json:"path,omitempty"`
	Body     string `json:"body,omitempty"`
	Query    string `json:"query,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
	Confirm  bool   `json:"confirm,omitempty"`
}

func RunCommand(settings config.InstructionSettings, arg string) (string, error) {
	input, err := ParseCommand(arg)
	if err != nil {
		return "", err
	}
	return Execute(settings, input)
}

func ParseCommand(arg string) (OperationInput, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return OperationInput{Op: "list"}, nil
	}
	op, rest, _ := strings.Cut(arg, " ")
	in := OperationInput{Op: strings.ToLower(strings.TrimSpace(op))}
	rest = strings.TrimSpace(rest)
	switch in.Op {
	case "list", "status":
		if rest == "" {
			return in, nil
		}
		values, err := parseOptionalKeyValues(rest)
		if err != nil {
			return OperationInput{}, err
		}
		applyCommandValues(&in, values)
		if in.Query == "" && len(values) == 0 {
			in.Query = rest
		}
	case "search":
		if rest == "" {
			return OperationInput{}, fmt.Errorf("memory search requires query text or query=...")
		}
		values, err := parseOptionalKeyValues(rest)
		if err != nil {
			return OperationInput{}, err
		}
		applyCommandValues(&in, values)
		if in.Query == "" && len(values) == 0 {
			in.Query = rest
		}
	case "read":
		if rest == "" {
			return OperationInput{}, fmt.Errorf("memory read requires topic=... or path=...")
		}
		values, err := parseOptionalKeyValues(rest)
		if err != nil {
			return OperationInput{}, err
		}
		applyCommandValues(&in, values)
		if len(values) == 0 {
			if strings.Contains(rest, "/") || strings.HasSuffix(strings.ToLower(rest), ".md") {
				in.Path = rest
			} else {
				in.Topic = rest
			}
		}
	case "add", "replace", "remove":
		values, err := parseKeyValues(rest)
		if err != nil {
			return OperationInput{}, err
		}
		applyCommandValues(&in, values)
	default:
		return OperationInput{}, fmt.Errorf("unknown memory operation %q", in.Op)
	}
	return in, nil
}

func Execute(settings config.InstructionSettings, in OperationInput) (string, error) {
	settings = commandSettings(settings)
	switch strings.ToLower(strings.TrimSpace(in.Op)) {
	case "", "list", "status":
		return List(settings, in)
	case "search":
		return Search(settings, in)
	case "read":
		return Read(settings, in)
	case "add":
		return upsert(settings, in, false)
	case "replace":
		return upsert(settings, in, true)
	case "remove":
		return remove(settings, in)
	default:
		return "", fmt.Errorf("unknown memory operation %q", in.Op)
	}
}

func List(settings config.InstructionSettings, in OperationInput) (string, error) {
	snapshot, err := Load(commandSettings(settings))
	if err != nil {
		return "", err
	}
	entries := filterEntries(settings, snapshot.Entries, in.Source, strings.TrimSpace(in.Query))
	limit := normalizeLimit(in.Limit, DefaultListLimit)
	var lines []string
	lines = append(lines, fmt.Sprintf("memory entries: %d", len(entries)))
	if len(snapshot.Roots) > 0 {
		lines = append(lines, "roots:")
		for _, root := range snapshot.Roots {
			lines = append(lines, fmt.Sprintf("- %s %s", root.Source, root.Dir))
		}
	}
	if len(entries) == 0 {
		if len(lines) == 1 {
			lines = append(lines, "no memory entries")
		}
		return strings.Join(lines, "\n"), nil
	}
	lines = append(lines, "entries:")
	for i, entry := range entries {
		if i >= limit {
			lines = append(lines, fmt.Sprintf("...[truncated at %d entries]", limit))
			break
		}
		lines = append(lines, entrySummaryLine(entry))
	}
	if names := capFlagNames(snapshot.Caps); len(names) > 0 {
		lines = append(lines, "cap_flags: "+strings.Join(names, ","))
	}
	return strings.Join(lines, "\n"), nil
}

func Search(settings config.InstructionSettings, in OperationInput) (string, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "", fmt.Errorf("memory search query required")
	}
	snapshot, err := Load(commandSettings(settings))
	if err != nil {
		return "", err
	}
	limit := normalizeLimit(in.Limit, DefaultSearchLimit)
	maxBytes := normalizeMaxBytes(in.MaxBytes, commandSettings(settings).MemoryTopicMaxBytes)
	lower := strings.ToLower(query)
	var lines []string
	for _, entry := range filterEntries(settings, snapshot.Entries, in.Source, "") {
		if len(lines) >= limit {
			break
		}
		haystack := strings.ToLower(strings.Join([]string{entry.Source, entry.Type, entry.Topic, entry.Summary, entry.Path}, " "))
		if strings.Contains(haystack, lower) {
			lines = append(lines, "summary "+entrySummaryLine(entry))
			continue
		}
		body, truncated, err := readTopic(entry, maxBytes)
		if err != nil {
			continue
		}
		if idx := strings.Index(strings.ToLower(body), lower); idx >= 0 {
			lines = append(lines, fmt.Sprintf("body %s match=%s", entryRef(entry), quote(snippet(body, idx, 120))))
			if truncated {
				lines[len(lines)-1] += " truncated=true"
			}
		}
	}
	if len(lines) == 0 {
		return "memory search: no matches", nil
	}
	if len(lines) >= limit {
		lines = append(lines, fmt.Sprintf("...[truncated at %d matches]", limit))
	}
	return "memory search results:\n" + strings.Join(lines, "\n"), nil
}

func Read(settings config.InstructionSettings, in OperationInput) (string, error) {
	snapshot, err := Load(commandSettings(settings))
	if err != nil {
		return "", err
	}
	entry, err := selectEntry(settings, snapshot.Entries, in)
	if err != nil {
		return "", err
	}
	maxBytes := normalizeMaxBytes(in.MaxBytes, commandSettings(settings).MemoryTopicMaxBytes)
	body, truncated, err := readTopic(entry, maxBytes)
	if err != nil {
		return "", err
	}
	lines := []string{
		"memory read:",
		entrySummaryLine(entry),
		"",
		strings.TrimRight(body, "\n"),
	}
	if truncated {
		lines = append(lines, fmt.Sprintf("[truncated at %d bytes]", maxBytes))
	}
	return strings.Join(lines, "\n"), nil
}

func upsert(settings config.InstructionSettings, in OperationInput, replace bool) (string, error) {
	root, err := rootForMutation(settings, in.Source)
	if err != nil {
		return "", err
	}
	entry, absPath, err := operationEntry(root, in)
	if err != nil {
		return "", err
	}
	body := in.Body
	if strings.TrimSpace(body) == "" {
		body = entry.Summary + "\n"
	}
	if len([]byte(body)) > commandSettings(settings).MemoryTopicMaxBytes {
		return "", fmt.Errorf("memory topic body is %d bytes; limit is %d", len([]byte(body)), commandSettings(settings).MemoryTopicMaxBytes)
	}
	entries, err := loadEditableRoot(root, commandSettings(settings))
	if err != nil {
		return "", err
	}
	match := findEntryIndex(entries, entry)
	op := "add"
	if replace {
		op = "replace"
	}
	if !replace && match >= 0 {
		return "", fmt.Errorf("memory entry already exists for topic/path; use replace")
	}
	if replace && match < 0 {
		return "", fmt.Errorf("memory entry not found for replace")
	}
	preview := mutationPreview(op, root, entry, body, in.Confirm)
	if !in.Confirm {
		return preview, nil
	}
	if err := os.MkdirAll(filepath.Dir(absPath), defaultMemoryDirMode); err != nil {
		return "", err
	}
	if err := writeFileAtomic(absPath, []byte(body), defaultMemoryFileMode); err != nil {
		return "", err
	}
	if replace {
		entries[match] = entry
	} else {
		entries = append(entries, entry)
	}
	if err := writeIndex(root.IndexPath, entries); err != nil {
		return "", err
	}
	return preview + "\nwritten=true", nil
}

func remove(settings config.InstructionSettings, in OperationInput) (string, error) {
	root, err := rootForMutation(settings, in.Source)
	if err != nil {
		return "", err
	}
	entries, err := loadEditableRoot(root, commandSettings(settings))
	if err != nil {
		return "", err
	}
	entry, idx, err := selectMutableEntry(entries, in)
	if err != nil {
		return "", err
	}
	_, absPath, err := validateEntryPath(root.Dir, entry.Path)
	if err != nil {
		return "", err
	}
	lines := []string{
		"memory remove preview",
		entrySummaryLine(entry),
		"confirm: " + strconv.FormatBool(in.Confirm),
	}
	if !in.Confirm {
		lines = append(lines, "not removed; rerun with confirm=true to remove index entry and topic file")
		return strings.Join(lines, "\n"), nil
	}
	entries = append(entries[:idx], entries[idx+1:]...)
	if err := writeIndex(root.IndexPath, entries); err != nil {
		return "", err
	}
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	lines = append(lines, "removed=true")
	return strings.Join(lines, "\n"), nil
}

func commandSettings(settings config.InstructionSettings) config.InstructionSettings {
	settings.MemoryEnabled = true
	if settings.MemorySummaryMaxBytes <= 0 {
		settings.MemorySummaryMaxBytes = DefaultSummaryMaxBytes
	}
	if settings.MemoryIndexMaxBytes <= 0 {
		settings.MemoryIndexMaxBytes = DefaultIndexMaxBytes
	}
	if settings.MemoryTopicMaxBytes <= 0 {
		settings.MemoryTopicMaxBytes = DefaultTopicMaxBytes
	}
	return settings
}

func operationEntry(root Root, in OperationInput) (Entry, string, error) {
	entry := Entry{
		Type:    strings.TrimSpace(in.Type),
		Topic:   strings.TrimSpace(in.Topic),
		Summary: strings.TrimSpace(in.Summary),
		Path:    strings.TrimSpace(in.Path),
		Source:  root.Source,
		RootDir: root.Dir,
	}
	if entry.Type == "" || entry.Topic == "" || entry.Summary == "" || entry.Path == "" {
		return Entry{}, "", fmt.Errorf("memory %s requires type, topic, summary, and path", strings.TrimSpace(in.Op))
	}
	summary, blocked, reason := sanitizeSummary(entry.Summary)
	if blocked {
		return Entry{}, "", fmt.Errorf("memory summary rejected: %s", reason)
	}
	entry.Summary = summary
	clean, abs, err := validateEntryPath(root.Dir, entry.Path)
	if err != nil {
		return Entry{}, "", err
	}
	entry.Path = clean
	return entry, abs, nil
}

func loadEditableRoot(root Root, settings config.InstructionSettings) ([]Entry, error) {
	info, err := os.Stat(root.IndexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("memory index %s is a directory", root.IndexPath)
	}
	if settings.MemoryIndexMaxBytes > 0 && info.Size() > int64(settings.MemoryIndexMaxBytes) {
		return nil, fmt.Errorf("memory index %s is %d bytes; limit is %d", root.IndexPath, info.Size(), settings.MemoryIndexMaxBytes)
	}
	raw, err := os.ReadFile(root.IndexPath)
	if err != nil {
		return nil, err
	}
	entries, _, err := parseIndex(root, raw, settings.MemoryTopicMaxBytes)
	return entries, err
}

func writeIndex(path string, entries []Entry) error {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type < entries[j].Type
		}
		return entries[i].Topic < entries[j].Topic
	})
	var b strings.Builder
	b.WriteString("# Billyharness memory index\n")
	b.WriteString("# Format: - type=user topic=style summary=\"Short summary\" path=topics/style.md\n\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, "- type=%s topic=%s summary=%s path=%s\n", shellQuote(entry.Type), shellQuote(entry.Topic), shellQuote(entry.Summary), shellQuote(entry.Path))
	}
	if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirMode); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(b.String()), defaultMemoryFileMode)
}

func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func readTopic(entry Entry, maxBytes int) (string, bool, error) {
	_, absPath, err := validateEntryPath(entry.RootDir, entry.Path)
	if err != nil {
		return "", false, err
	}
	file, err := os.Open(absPath)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	if maxBytes <= 0 {
		maxBytes = DefaultReadMaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(raw) > maxBytes
	if truncated {
		raw = raw[:maxBytes]
	}
	return trimUTF8(string(raw), len(raw)), truncated, nil
}

func rootForMutation(settings config.InstructionSettings, source string) (Root, error) {
	settings = commandSettings(settings)
	source = normalizeSource(source, settings)
	for _, root := range Roots(settings) {
		if root.Source == source {
			return root, nil
		}
	}
	return Root{}, fmt.Errorf("memory source %q is not available", source)
}

func normalizeSource(source string, settings config.InstructionSettings) string {
	source = strings.TrimSpace(strings.ToLower(source))
	switch source {
	case "", "home", "global":
		return "home"
	case "profile":
		if settings.Profile.Profile != "" {
			return "profile:" + config.NormalizeProfileName(settings.Profile.Profile)
		}
		return source
	default:
		if strings.HasPrefix(source, "profile:") {
			name := strings.TrimSpace(strings.TrimPrefix(source, "profile:"))
			return "profile:" + config.NormalizeProfileName(name)
		}
		return source
	}
}

func filterEntries(settings config.InstructionSettings, entries []Entry, source, query string) []Entry {
	source = strings.TrimSpace(source)
	query = strings.ToLower(strings.TrimSpace(query))
	var out []Entry
	for _, entry := range entries {
		if source != "" && normalizeSource(source, settings) != entry.Source {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{entry.Source, entry.Type, entry.Topic, entry.Summary, entry.Path}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

func selectEntry(settings config.InstructionSettings, entries []Entry, in OperationInput) (Entry, error) {
	matches := matchingEntries(settings, entries, in)
	switch len(matches) {
	case 0:
		return Entry{}, fmt.Errorf("memory entry not found")
	case 1:
		return matches[0], nil
	default:
		return Entry{}, fmt.Errorf("memory selector is ambiguous (%d matches); add source= or path=", len(matches))
	}
}

func selectMutableEntry(entries []Entry, in OperationInput) (Entry, int, error) {
	matches := matchingEntries(config.InstructionSettings{}, entries, in)
	if len(matches) == 0 {
		return Entry{}, -1, fmt.Errorf("memory entry not found")
	}
	if len(matches) > 1 {
		return Entry{}, -1, fmt.Errorf("memory selector is ambiguous (%d matches); add path=", len(matches))
	}
	for i, entry := range entries {
		if entry.Path == matches[0].Path && entry.Topic == matches[0].Topic {
			return entry, i, nil
		}
	}
	return Entry{}, -1, fmt.Errorf("memory entry not found")
}

func matchingEntries(settings config.InstructionSettings, entries []Entry, in OperationInput) []Entry {
	var out []Entry
	source := strings.TrimSpace(in.Source)
	for _, entry := range entries {
		if source != "" && normalizeSource(source, settings) != entry.Source {
			continue
		}
		switch {
		case strings.TrimSpace(in.Path) != "" && entry.Path == strings.TrimSpace(in.Path):
			out = append(out, entry)
		case strings.TrimSpace(in.Topic) != "" && strings.EqualFold(entry.Topic, strings.TrimSpace(in.Topic)):
			out = append(out, entry)
		}
	}
	return out
}

func findEntryIndex(entries []Entry, want Entry) int {
	for i, entry := range entries {
		if entry.Path == want.Path || strings.EqualFold(entry.Topic, want.Topic) {
			return i
		}
	}
	return -1
}

func mutationPreview(op string, root Root, entry Entry, body string, confirm bool) string {
	lines := []string{
		"memory " + op + " preview",
		"source: " + root.Source,
		"index: " + root.IndexPath,
		entrySummaryLine(entry),
		fmt.Sprintf("topic_body_bytes: %d", len([]byte(body))),
		"confirm: " + strconv.FormatBool(confirm),
	}
	if !confirm {
		lines = append(lines, "not written; rerun with confirm=true to write")
	}
	return strings.Join(lines, "\n")
}

func entrySummaryLine(entry Entry) string {
	parts := []string{
		entryRef(entry),
		"type=" + entry.Type,
		"summary=" + quote(entry.Summary),
	}
	if entry.TopicBytes > 0 {
		parts = append(parts, fmt.Sprintf("bytes=%d", entry.TopicBytes))
	}
	if entry.TopicCapped {
		parts = append(parts, "capped=true")
	}
	if entry.TopicMissing {
		parts = append(parts, "missing=true")
	}
	return "- " + strings.Join(parts, " ")
}

func entryRef(entry Entry) string {
	return fmt.Sprintf("source=%s topic=%s path=%s", entry.Source, entry.Topic, entry.Path)
}

func parseOptionalKeyValues(rest string) (map[string]string, error) {
	if !strings.Contains(rest, "=") {
		return nil, nil
	}
	return parseKeyValues(rest)
}

func applyCommandValues(in *OperationInput, values map[string]string) {
	if len(values) == 0 {
		return
	}
	in.Source = firstValue(values, "source", in.Source)
	in.Type = firstValue(values, "type", in.Type)
	in.Topic = firstValue(values, "topic", in.Topic)
	in.Summary = firstValue(values, "summary", in.Summary)
	in.Path = firstValue(values, "path", in.Path)
	in.Body = firstValue(values, "body", in.Body)
	in.Query = firstValue(values, "query", in.Query)
	if value := firstValue(values, "limit", ""); value != "" {
		in.Limit, _ = strconv.Atoi(value)
	}
	if value := firstValue(values, "max_bytes", ""); value != "" {
		in.MaxBytes, _ = strconv.Atoi(value)
	}
	if value := firstValue(values, "confirm", ""); value != "" {
		in.Confirm = parseBool(value)
	}
}

func firstValue(values map[string]string, key, fallback string) string {
	if value, ok := values[key]; ok {
		return value
	}
	return fallback
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "confirm", "confirmed":
		return true
	default:
		return false
	}
}

func normalizeLimit(value, fallback int) int {
	if value <= 0 {
		value = fallback
	}
	if value > 200 {
		value = 200
	}
	return value
}

func normalizeMaxBytes(value, fallback int) int {
	if value <= 0 {
		value = fallback
	}
	if value <= 0 {
		value = DefaultReadMaxBytes
	}
	if value > MaxReadMaxBytes {
		value = MaxReadMaxBytes
	}
	return value
}

func shellQuote(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\"\\") {
		return strconv.Quote(value)
	}
	return value
}

func snippet(body string, idx, limit int) string {
	if idx < 0 {
		idx = 0
	}
	start := idx - limit/3
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > len(body) {
		end = len(body)
	}
	return strings.TrimSpace(body[start:end])
}
